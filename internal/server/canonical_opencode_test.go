package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/platforms/platformtest"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

// nativeOpenCode registers a native adapter for host-wiring tests that never
// read persisted sessions; its optional capabilities stay visible.
type nativeOpenCode struct {
	*opencodeplatform.Adapter
	platformtest.NoBrowsing
}

// canonicalOpenCodeRegistry composes a native OpenCode integration the way
// startup does: core migration and a canonical adapter. The registry is not
// enforced so tests can add fake platforms beside it.
func canonicalOpenCodeRegistry(t *testing.T, database *db.DB, stateDB *state.DB) (*platforms.Registry, *statev2.DB) {
	t.Helper()
	history := statev2test.Open(t)
	catalogue, err := sessioncatalog.New(stateDB)
	if err != nil {
		t.Fatal(err)
	}
	native := opencodeplatform.New(database, stateDB, nil, ocapi.Auth{})
	adapters, err := owned.BuildCore(t.Context(), stateDB, history, catalogue, "owner", []platforms.Integration{native})
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.PrepareBuiltCore(t.Context(), stateDB, history, catalogue, "owner", adapters); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	for _, adapter := range adapters {
		registry.Register(adapter)
	}
	return registry, history
}

var ingesting sync.Map // *Server whose ingestion a test started

// ingestHistory runs the server's ingestion until the read model holds want
// OpenCode sessions, as the background worker does in production.
func ingestHistory(t *testing.T, s *Server, history *statev2.DB, want int) {
	t.Helper()
	if s.historyIngest == nil {
		t.Fatal("server has no history ingestion")
	}
	if _, running := ingesting.LoadOrStore(s, true); !running {
		if s.stateDB != nil {
			if err := s.taskManager().Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.taskManager().Stop)
		}
		if err := s.historyIngest.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.historyIngest.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done; ingesting.Delete(s) })
	}
	s.historyIngest.Notify(string(opencodeplatform.PlatformID), "")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		instances, err := history.Instances(t.Context(), "owner", string(opencodeplatform.PlatformID))
		got := 0
		if err == nil && len(instances) == 1 {
			rows, err := history.Sessions(t.Context(), instances[0].ID)
			if err == nil {
				got = len(rows)
			}
		}
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ingested %d OpenCode sessions, want %d", got, want)
		}
	}
}

// canonicalID returns the Ocman identity ingestion bound to an OpenCode native
// session seeded in the test database.
func canonicalID(t *testing.T, s *Server, nativeID string) string {
	t.Helper()
	adapter, ok := s.registry.Get(opencodeplatform.PlatformID)
	if !ok {
		t.Fatal("no OpenCode adapter")
	}
	source, err := adapter.(platforms.HistoryReader).HistoryIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: source.Platform, SourceKey: source.SourceKey, NativeID: nativeID})
	if err != nil {
		t.Fatalf("no identity for %s: %v", nativeID, err)
	}
	return row.ID
}
