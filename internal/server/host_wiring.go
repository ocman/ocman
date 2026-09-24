package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
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
	return hostlocal.New(hostlocal.Deps{
		LaunchTmux:   s.gateLaunchTmux(tmux.LaunchOpencode),
		Runtime:      s.gatedRuntime(),
		DiscoverPort: opencode.DiscoverOpenCodePortFresh,
		ManagedStore: managedStoreOrNil(s.stateDB),
		// CreateSession routes worktree-session creation through the shared
		// session-mutation service (same validated path + hooks as REST/gRPC).
		// Resolved lazily: s.sessions is assigned after newLocalHost runs.
		CreateSession: func(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return s.sessions.Client("opencode").CreateSession(ctx, req)
		},
		CreateConfiguredSession: func(ctx context.Context, req platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
			return s.sessions.CreateConfigured(ctx, "opencode", req, rules)
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
	})
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
