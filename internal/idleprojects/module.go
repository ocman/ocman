// Package idleprojects stops managed per-project instances after their normal
// sessions have been archived and all background work has settled.
package idleprojects

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

const setting = "projects.idle_timeout_minutes"

type Target struct {
	Platform platforms.Platform
	Runtime  hostsvc.IdleProjectRuntime
}

type Deps struct {
	Store   *state.DB
	Targets func() []Target
}

type observation struct {
	since time.Time
	stamp string
}

type Module struct {
	deps Deps
	// ponytail: process-local timers restart after ocman restarts. No migration
	// is needed for a cleanup optimization; persist only if restart churn matters.
	idle map[state.Key]observation
}

func New(deps Deps) *Module    { return &Module{deps: deps, idle: map[state.Key]observation{}} }
func (m *Module) Name() string { return "idle-projects" }

func (m *Module) Routes(r module.Router) {
	r.LocalSetting("/api/settings/idle-project-timeout", m.settings)
}

func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := m.sweep(ctx, time.Now()); err != nil {
			log.WithError(err).Warn("idle projects: sweep")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) timeout(ctx context.Context) (time.Duration, error) {
	value, _, err := m.deps.Store.GetSetting(ctx, setting)
	if err != nil || value == "" {
		return 0, err
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes < 0 || minutes > 43200 {
		return 0, fmt.Errorf("invalid idle project timeout %q", value)
	}
	return time.Duration(minutes) * time.Minute, nil
}

func (m *Module) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Minutes int `json:"minutes"`
		}
		if !httpx.ReadJSON(w, r, 1024, &req) {
			return
		}
		if req.Minutes < 0 || req.Minutes > 43200 {
			http.Error(w, "minutes must be between 0 (Never) and 43200", http.StatusBadRequest)
			return
		}
		if err := m.deps.Store.SetSetting(r.Context(), setting, strconv.Itoa(req.Minutes)); err != nil {
			httpx.ServerError(w, "saving idle project timeout", err)
			return
		}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	timeout, err := m.timeout(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading idle project timeout", err)
		return
	}
	httpx.WriteJSON(w, map[string]int64{"minutes": int64(timeout / time.Minute)})
}

func (m *Module) sweep(ctx context.Context, now time.Time) error {
	timeout, err := m.timeout(ctx)
	if err != nil || timeout == 0 {
		clear(m.idle)
		return err
	}
	next := map[state.Key]observation{}
	defer func() { m.idle = next }()
	for _, target := range m.deps.Targets() {
		roots, err := target.Runtime.ManagedIdleProjects(ctx)
		if err != nil {
			return err
		}
		for _, root := range roots {
			key := state.Key{Platform: string(target.Platform.ID()), SessionID: root}
			_, stamp, idle, err := m.eligible(ctx, target, root, false)
			if err != nil {
				log.WithError(err).WithField("project", root).Warn("idle projects: checking eligibility")
				continue // An uncertain read resets the grace period.
			}
			if !idle {
				continue
			}
			observed := m.idle[key]
			if observed.since.IsZero() || observed.stamp != stamp {
				observed = observation{since: now, stamp: stamp}
			}
			next[key] = observed
			if now.Sub(observed.since) < timeout {
				continue
			}
			stopped, err := target.Runtime.CullIdleProject(ctx, root, func(ctx context.Context) (bool, error) {
				_, current, idle, err := m.eligible(ctx, target, root, true)
				return idle && current == stamp, err
			})
			if err != nil {
				log.WithError(err).WithField("project", root).Warn("idle projects: stopping instance")
			}
			delete(next, key) // A refused stop starts a new grace period too.
			if stopped {
				log.WithFields(log.Fields{"project": root, "platform": key.Platform}).Info("idle projects: stopped instance")
			}
		}
	}
	return nil
}

func active(status db.SessionStatus) bool {
	return status == db.StatusBusy || status == db.StatusWaiting || status == db.StatusLimited
}

func (m *Module) eligible(ctx context.Context, target Target, root string, fresh bool) ([]db.Session, string, bool, error) {
	platform := target.Platform
	resolved := map[string]string{}
	resolve := func(directory string) (string, error) {
		if root, ok := resolved[directory]; ok {
			return root, nil
		}
		root, err := target.Runtime.ProjectRoot(ctx, directory)
		if err == nil {
			resolved[directory] = root
		}
		return root, err
	}
	all, err := platform.Sessions(ctx, "", 0)
	if err != nil {
		return nil, "", false, err
	}
	var sessions []db.Session
	for _, session := range all {
		owner, err := resolve(session.Directory)
		if err != nil {
			return nil, "", false, err
		}
		if owner != root {
			continue
		}
		if reader, ok := platform.(platforms.LifecycleReader); fresh && ok {
			lifecycle, err := reader.SessionLifecycle(ctx, session.ID)
			if err != nil {
				return nil, "", false, err
			}
			if lifecycle == nil {
				return nil, "", false, nil
			}
			session.Status = lifecycle.Status
			session.TimeUpdated = max(session.TimeUpdated, lifecycle.LatestMessageCreated)
		} else if fresh {
			detail, err := platform.Session(ctx, session.ID, 1, 0)
			if err != nil {
				return nil, "", false, err
			}
			if detail == nil || detail.Session == nil {
				return nil, "", false, nil
			}
			session = *detail.Session
			for _, child := range detail.SessionTree {
				if active(child.Status) {
					return nil, "", false, nil
				}
			}
		}
		if active(session.Status) {
			return nil, "", false, nil
		}
		sessions = append(sessions, session)
	}
	// Read intent and work claims after the live reads: opening a session or
	// claiming a routine while those requests are in flight must prevent culling.
	archives, err := m.deps.Store.ArchivedSessions(ctx)
	if err != nil {
		return nil, "", false, err
	}
	routines, err := m.deps.Store.RoutineSessions(ctx)
	if err != nil {
		return nil, "", false, err
	}
	factory, err := m.deps.Store.FactorySessions(ctx)
	if err != nil {
		return nil, "", false, err
	}
	if work, err := m.backgroundWork(ctx, root, resolve); err != nil || work {
		return nil, "", false, err
	}
	var stamps []string
	for _, session := range sessions {
		key := state.Key{Platform: string(platform.ID()), SessionID: session.ID}
		archived, exists := archives[key]
		normal := session.ParentID == "" && routines[key] == "" && factory[key] == "" && !strings.HasSuffix(session.Title, " subagent)")
		if normal && (!exists || session.TimeUpdated > archived) {
			return nil, "", false, nil
		}
		queued, err := m.deps.Store.ListQueuedMessages(ctx, key.Platform, key.SessionID)
		if err != nil {
			return nil, "", false, err
		}
		if len(queued) != 0 {
			return nil, "", false, nil
		}
		stamps = append(stamps, fmt.Sprintf("%s:%d:%d", session.ID, session.TimeUpdated, archived))
	}
	slices.Sort(stamps)
	return sessions, strings.Join(stamps, ";"), len(sessions) != 0, nil
}

func (m *Module) backgroundWork(ctx context.Context, root string, resolve func(string) (string, error)) (bool, error) {
	runs, err := m.deps.Store.ListRunningRoutineRuns(ctx)
	if err != nil {
		return false, err
	}
	for _, run := range runs {
		if state.NormalizeRemoteID(run.RemoteID) == state.LocalRemoteID {
			owner, err := resolve(run.Directory)
			if err != nil {
				return false, err
			}
			if owner == root {
				return true, nil
			}
		}
	}
	attempts, err := m.deps.Store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return false, err
	}
	for _, attempt := range attempts {
		if attempt.Phase == "prepared" || attempt.Phase == "active" || attempt.Phase == "stopping" {
			for _, directory := range append([]string{attempt.FrozenPolicy.Repository}, attempt.FrozenPolicy.Projects...) {
				if directory == "" {
					continue
				}
				owner, err := resolve(directory)
				if err != nil {
					return false, err
				}
				if owner == root {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
