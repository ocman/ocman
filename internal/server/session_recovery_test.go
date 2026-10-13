package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestRecoveryHooksResolveCurrentServerDependencies(t *testing.T) {
	srv, _ := newInterruptionTestServer(t)
	// Host callbacks capture these methods before startup finishes wiring the
	// server. Their service must resolve the database and adapter on invocation.
	record, confirm := srv.recordOpencodeReplacement, srv.confirmOpencodeReplacement
	original := srv.stateDB
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv.stateDB = store
	srv.registry = platforms.NewRegistry()
	srv.registry.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "fresh", Directory: "/checkout"}}},
		lifecycle: func(string) (*platforms.SessionLifecycle, error) {
			return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "turn"}, nil
		},
	})
	srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(context.Context, string) (string, error) { return "/repo", nil }})
	if err := record(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := confirm(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	notices, err := store.SessionInterruptions(t.Context(), "opencode", "fresh")
	if err != nil || len(notices) != 1 || notices[0].MessageID != "turn" {
		t.Fatalf("current dependencies not used: %v %v", notices, err)
	}
	notices, err = original.SessionInterruptions(t.Context(), "opencode", "fresh")
	if err != nil || len(notices) != 0 {
		t.Fatalf("old store used: %v %v", notices, err)
	}
}
