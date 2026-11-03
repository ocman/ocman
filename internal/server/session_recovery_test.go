package server

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestRecoveryHooksResolveCurrentServerDependencies(t *testing.T) {
	srv, _ := newInterruptionTestServer(t)
	// Host callbacks capture these methods before startup finishes wiring the
	// server. Their service must resolve the database and adapter on invocation.
	record, confirm := srv.recordOpencodeReplacement, srv.confirmOpencodeReplacement
	original := srv.stateDB
	store := statetest.Open(t, ModuleSchemas()...)
	srv.stateDB = store
	srv.registry = platforms.NewRegistry()
	srv.registry.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{PlatformID: "opencode", SessionList: []db.Session{{ID: "fresh", Directory: "/checkout"}}},
		lifecycle: func(string) (*platforms.SessionLifecycle, error) {
			return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "turn"}, nil
		},
	})
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
