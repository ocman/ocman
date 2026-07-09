package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/loops"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	// loopEngineTickInterval matches the existing watcher cadence; each
	// trigger self-throttles (AD-2) so a short tick is fine.
	loopEngineTickInterval = 5 * time.Second

	// loopEngineWorkers bounds concurrent loop evaluation so one slow
	// loop (forge call, spawn) cannot starve others (AD-1 / NFR-2).
	loopEngineWorkers = 4
)

// loopServiceFn builds the loop Service lazily, so tests can override it.
// nil means "construct the production service".
var loopServiceFn func(s *Server) *loops.Service

// loopSvc returns the server's loop Service, building it on first use.
func (s *Server) loopSvc() *loops.Service {
	s.loopSvcOnce.Do(func() {
		if loopServiceFn != nil {
			s.loopSvcCached = loopServiceFn(s)
			return
		}
		s.loopSvcCached = loops.NewService(loops.Deps{
			Store:     s.stateDB,
			Messenger: &loopMessenger{s: s},
			Launcher:  &loopLauncher{s: s},
			Forge:     &loopForge{s: s},
			Status:    &loopStatusInferer{s: s},
			Usage:     &loopUsage{s: s},
			Dirs:      &loopDirResolver{s: s},
			Notify:    func(loopID string) { s.broadcastLoopUpdated(loopID) },
		})
	})
	return s.loopSvcCached
}

// runLoopEngine is the agent-loops engine goroutine (AD-1). It ticks on a
// fixed interval, loads active loops, and dispatches each to a bounded
// worker pool. A per-loop in-flight guard prevents a long action from
// double-firing on the next tick; durable idempotency comes from the
// pending loop_iterations row (AD-5a).
func (s *Server) runLoopEngine(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	ticker := time.NewTicker(loopEngineTickInterval)
	defer ticker.Stop()

	inflight := &inflightSet{m: map[string]bool{}}
	sem := make(chan struct{}, loopEngineWorkers)

	tick := func() { s.loopEngineTick(ctx, inflight, sem) }
	runWithRecover("loop-engine", tick)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("loop-engine", tick)
		}
	}
}

// loopEngineTick evaluates every active loop once.
func (s *Server) loopEngineTick(ctx context.Context, inflight *inflightSet, sem chan struct{}) {
	active, err := s.stateDB.ListActiveLoops()
	if err != nil {
		log.WithError(err).Warn("loop-engine: listing active loops")
		return
	}
	svc := s.loopSvc()
	var wg sync.WaitGroup
	for _, l := range active {
		if !inflight.acquire(l.ID) {
			continue // already being evaluated
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(loop state.Loop) {
			defer wg.Done()
			defer func() { <-sem }()
			defer inflight.release(loop.ID)
			runWithRecover("loop-engine-eval", func() {
				if _, err := svc.EvaluateOne(ctx, loop); err != nil {
					log.WithFields(log.Fields{"loopID": loop.ID, "error": err}).
						Warn("loop-engine: evaluate")
				}
			})
		}(l)
	}
	wg.Wait()
}

// inflightSet tracks loops currently being evaluated (AD-1 in-flight guard).
type inflightSet struct {
	mu sync.Mutex
	m  map[string]bool
}

func (s *inflightSet) acquire(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[id] {
		return false
	}
	s.m[id] = true
	return true
}

func (s *inflightSet) release(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

// --- adapters: bridge loops.* interfaces to server dependencies ---

// loopMessenger implements loops.Messenger via the platform registry.
type loopMessenger struct{ s *Server }

func (m *loopMessenger) SendPrompt(ctx context.Context, sessionID, prompt, model, agent, reasoning string) error {
	return m.s.sessions.SendMessage(ctx, "", platforms.SendMessageRequest{
		SessionID: sessionID,
		Message:   prompt,
		Model:     model,
		Agent:     agent,
		Reasoning: reasoning,
	})
}

// loopDirResolver implements loops.SessionDirResolver via the platform
// registry, so a loop created without an explicit directory backfills it
// from its root session's working directory.
type loopDirResolver struct{ s *Server }

func (r *loopDirResolver) SessionDir(ctx context.Context, sessionID string) (string, bool) {
	p, found := r.s.registry.PlatformForSession(ctx, sessionID)
	if !found {
		return "", false
	}
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil || detail.Session.Directory == "" {
		return "", false
	}
	return detail.Session.Directory, true
}

// loopStatusInferer implements loops.SessionStatusInferer by reusing the
// same status inference the child-session watcher uses.
type loopStatusInferer struct{ s *Server }

func (i *loopStatusInferer) TurnRunning(ctx context.Context, _ string, sessionID string) (running, ok bool) {
	p, found := i.s.registry.PlatformForSession(ctx, sessionID)
	if !found {
		return false, false
	}
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return false, false
	}
	return detail.Session.Status == "busy", true
}

// loopUsage implements loops.UsageSource by summing per-session
// token/cost from the platform adapter (the same TotalCost/Tokens the
// stats pages read). Sessions that can't be resolved are skipped; ok is
// true as long as at least one session resolved, so a single deleted
// child doesn't blank out the whole budget.
type loopUsage struct{ s *Server }

func (u *loopUsage) SessionUsage(ctx context.Context, sessionIDs []string) (int64, float64, bool) {
	var tokens int64
	var cost float64
	var any bool
	for _, id := range sessionIDs {
		p, found := u.s.registry.PlatformForSession(ctx, id)
		if !found {
			continue
		}
		detail, err := p.Session(ctx, id, 1, 0)
		if err != nil || detail == nil || detail.Session == nil {
			continue
		}
		tokens += detail.Session.TotalInputTokens + detail.Session.TotalOutputTokens
		cost += detail.Session.TotalCost
		any = true
	}
	return tokens, cost, any
}

// loopLauncher implements loops.Launcher via mcp.SessionLauncher (AD-5).
type loopLauncher struct{ s *Server }

func (l *loopLauncher) Spawn(ctx context.Context, req loops.SpawnRequest) (string, error) {
	launcher := l.s.newSessionLauncher()
	if launcher == nil {
		return "", fmt.Errorf("opencode platform not registered")
	}
	lr := internalmcp.LaunchRequest{
		ParentSessionID: req.ParentSession,
		Platform:        req.Platform,
		Directory:       req.Directory,
		Intent:          req.Intent,
		ComposedPrompt:  req.Prompt,
		Model:           req.Model,
		Agent:           req.Agent,
		Reasoning:       req.Reasoning,
		PermissionRules: req.PermissionRules,
		LoopID:          req.LoopID,
	}
	if !req.Worktree {
		return launcher.Launch(ctx, lr)
	}
	repoRoot, err := git.ResolveRepoRoot(ctx, req.Directory) // ocman:allow-host-helper
	if err != nil {
		return "", fmt.Errorf("resolving repo root: %w", err)
	}
	branch := fmt.Sprintf("ocman/loop-%s-%d", shortID(req.LoopID), time.Now().Unix())
	childID, _, err := launcher.LaunchWithWorktree(ctx, lr, git.CreateWorktreeRequest{
		RepoRoot:  repoRoot,
		Branch:    branch,
		NewBranch: true,
		BaseRef:   git.ResolveBaseRef(ctx, repoRoot), // ocman:allow-host-helper
	})
	return childID, err
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// loopForge implements loops.ForgePoller by reusing the PR-sidebar forge
// clients (AD-3: poll, no webhook). It detects head-SHA change and merge;
// comment polling is a future enhancement (the trigger already supports it).
type loopForge struct{ s *Server }

func (f *loopForge) PollPR(ctx context.Context, directory string, prNumber int) (loops.PRState, error) {
	_, remotes, err := f.s.detectUpstreams(ctx, directory)
	if err != nil {
		return loops.PRState{}, err
	}
	if len(remotes) == 0 {
		return loops.PRState{}, fmt.Errorf("no upstream forge detected for %s", directory)
	}
	fg, ok := f.s.resolveForge(remotes[0])
	if !ok {
		return loops.PRState{}, fmt.Errorf("could not resolve forge for %s", remotes[0].Host)
	}
	pr, err := f.s.fetchSinglePR(ctx, fg, remotes[0].Repo, prNumber)
	if err != nil {
		return loops.PRState{}, err
	}
	return loops.PRState{
		HeadSHA: pr.HeadSHA,
		Merged:  pr.Status == "merged",
	}, nil
}
