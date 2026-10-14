package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/term"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// newLocalHost builds the in-process hostsvc.Host, wiring the tmux,
// projects, and capability operations that live in this package into the
// dependency-injected local Host (which owns the the git package call
// sites directly). See internal/hostsvc/local.
func (s *Server) newLocalHost() hostsvc.Host {
	runtimes := map[string]func(context.Context, string) (*hostsvc.EnsureRuntimeResult, error){}
	namers := map[string]func(context.Context, string, string) (string, error){}
	if s.registry != nil {
		for _, adapter := range s.registry.Platforms() {
			if runtime, ok := adapter.(hostsvc.RuntimeEnsurer); ok {
				runtimes[string(adapter.ID())] = runtime.EnsureRuntime
			}
			if namer, ok := adapter.(hostsvc.WorktreeNamer); ok {
				namers[string(adapter.ID())] = namer.WorktreeName
			}
		}
	}
	host := hostlocal.New(hostlocal.Deps{
		DefaultPlatform: s.stateDB.DefaultPlatform,
		Runtimes:        runtimes,
		WorktreeNamers:  namers,
		HookPlugin:      func() hostsvc.HookPluginManager { return s.hookPlugin() },
		PlatformEnabled: func(id string) bool {
			if s.registry == nil {
				return false
			}
			_, ok := s.registry.Get(platforms.ID(id))
			return ok
		},
		LaunchTmux:            s.gateLaunchTmux(tmux.LaunchOpencode),
		Runtime:               s.gatedRuntime(),
		BeforeReplace:         s.recordOpencodeReplacement,
		BeforeStop:            s.beginOpencodeReplacementStopWithInstance,
		AfterStop:             s.confirmOpencodeReplacement,
		ReplacementStopped:    s.opencodeReplacementStopped,
		ReplacementStopping:   s.opencodeReplacementStopping,
		CancelReplacementStop: s.cancelOpencodeReplacementStop,
		// OpenCode v2: one server per machine, published to discovery.
		SetMachineServer:        opencode.SetMachineServer,
		ManagedStore:            managedStoreOrNil(s.stateDB),
		OpenCodeAuth:            func() ocapi.Auth { return s.openCodeAuth },
		OpenCodeReloaded:        opencode.InvalidateCatalogsForPort,
		RuntimeInventoryChanged: opencode.InvalidateOpenCodePortCache,
		// CreateSession routes worktree-session creation through the shared
		// session-mutation service (same validated path + hooks as REST/gRPC).
		// Resolved lazily: s.sessions is assigned after newLocalHost runs.
		CreatePlatformSession: func(ctx context.Context, id string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
			if rules != nil {
				return s.sessions.CreateConfigured(ctx, id, req, rules)
			}
			return s.sessions.Client(id).CreateSession(ctx, req)
		},
		TmuxSessions:     s.hostTmuxSessions,
		Projects:         s.hostProjects,
		ProjectUpstreams: s.hostProjectUpstreams,
		FetchPRHead:      s.hostFetchPRHead,
		Caps:             s.hostCaps,
		TermWindows:      term.Windows,
		TermCreateWindow: term.CreateWindow,
		TermKillWindow:   term.KillWindow,
		TermAttach:       term.AttachLocalPTY,
		StateDir:         stateDirOrEmpty(s.stateDB),
	})
	opencode.SetManagedPortSource(host.ManagedOpenCodePorts)
	return host
}

// Runtime rows can be absent after a failed confirmation. The owner's durable
// replacement phase, not a cleanup handle, decides whether to reconcile first.
func (s *Server) opencodeReplacementStopped(ctx context.Context, root string) (bool, error) {
	if s.stateDB == nil {
		return false, nil
	}
	return s.stateDB.ReplacementStopped(ctx, "opencode", root)
}

// stateDirOrEmpty returns the ocman data directory when a state DB is
// open, so the doctor only checks a directory ocman actually uses.
func stateDirOrEmpty(db *state.DB) string {
	if db == nil {
		return ""
	}
	return state.DefaultDataDir()
}

// managedStore adapts state.DB to hostlocal.ManagedStore, converting
// between the host's ManagedInstance and the state layer's decoupled
// plain struct. Returns nil when there is no state DB so the host falls
// back to its in-memory map (soft: a missing store never breaks launch).
type managedStore struct{ db *state.DB }

func managedStoreOrNil(db *state.DB) hostlocal.ManagedStore {
	if db == nil {
		return nil
	}
	return managedStore{db: db}
}

func (m managedStore) Upsert(ctx context.Context, repoRoot string, inst hostlocal.ManagedInstance) error {
	return m.db.UpsertManagedOpencode(ctx, repoRoot, state.ManagedInstance{
		Endpoint:   inst.Endpoint,
		Kind:       inst.Kind,
		RuntimeID:  inst.RuntimeID,
		PID:        inst.PID,
		LaunchedAt: inst.LaunchedAt,
	}, inst.LaunchedAt)
}

func (m managedStore) Get(ctx context.Context, repoRoot string) (hostlocal.ManagedInstance, bool, error) {
	mi, ok, err := m.db.GetManagedOpencode(ctx, repoRoot)
	if err != nil || !ok {
		return hostlocal.ManagedInstance{}, ok, err
	}
	return hostlocal.ManagedInstance{
		Endpoint:   mi.Endpoint,
		Kind:       mi.Kind,
		RuntimeID:  mi.RuntimeID,
		PID:        mi.PID,
		LaunchedAt: mi.LaunchedAt,
	}, true, nil
}

func (m managedStore) Delete(ctx context.Context, repoRoot string) error {
	return m.db.DeleteManagedOpencode(ctx, repoRoot)
}

func (m managedStore) List(ctx context.Context) (map[string]hostlocal.ManagedInstance, error) {
	instances, err := m.db.ManagedOpencodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]hostlocal.ManagedInstance, len(instances))
	for root, inst := range instances {
		out[root] = hostlocal.ManagedInstance{Endpoint: inst.Endpoint, Kind: inst.Kind, RuntimeID: inst.RuntimeID, PID: inst.PID, LaunchedAt: inst.LaunchedAt}
	}
	return out, nil
}
