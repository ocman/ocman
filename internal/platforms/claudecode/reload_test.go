package claudecode

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor/providertest"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func reloadTestAdapter(t *testing.T, sessions func(http.ResponseWriter, *http.Request)) (*provider.Adapter, string) {
	t.Helper()
	socket := filepath.Join(providertest.Directory(t), "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hello" {
			_ = json.NewEncoder(w).Encode(providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "claudecode", CLIVersion: "test"})
			return
		}
		sessions(w, r)
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	adapter, err := provider.New(t.Context(), socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adapter.Close)
	return adapter, socket
}

func TestReloadIdleDoesNotTrustBestEffortBrowsing(t *testing.T) {
	browse, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]db.Session{{ID: "old", Status: db.StatusDone}})
	})
	project, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	p := &claudePlatform{Adapter: browse, projects: &claudeProjects{roots: map[string]*claudeProject{"/repo": {adapter: project}}}}
	rows, err := p.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || rows[0].Status != db.StatusDone {
		t.Fatalf("browsing: %v %v", rows, err)
	}
	if idle, err := p.ReloadIdle(t.Context()); idle || err == nil {
		t.Fatalf("status failure hidden: %v %v", idle, err)
	}
	failedBrowser, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	p.Adapter = failedBrowser
	if idle, err := p.ReloadIdle(t.Context()); idle || err == nil {
		t.Fatalf("browsing failure hidden: %v %v", idle, err)
	}
}

func TestReloadIdleInspectsUnindexedAndRecoveringProjects(t *testing.T) {
	browse, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]db.Session{}) })
	for _, session := range []db.Session{
		{ID: "not-indexed", Status: db.StatusBusy}, {ID: "prompt", Status: db.StatusDone, PendingQuestion: true}, {ID: "idle", Status: db.StatusDone},
	} {
		project, socket := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]db.Session{session}) })
		store, err := state.Open(statetest.Path(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if err := store.UpsertManagedProvider(t.Context(), "claudecode", "/repo", state.ManagedInstance{Endpoint: "unix://" + socket}); err != nil {
			t.Fatal(err)
		}
		p := &claudePlatform{Adapter: browse, projects: &claudeProjects{
			roots: map[string]*claudeProject{},
			mgr:   &hostlocal.ProviderManager{State: store, Runtime: ocruntime.NewBackendRuntime("claudecode", "", "")},
		}}
		idle, err := p.ReloadIdle(t.Context())
		if err != nil || idle == platforms.ReloadSessionActive(session) {
			t.Fatalf("recovering supervisor: %v %v", idle, err)
		}
		p.projects.roots["/repo"] = &claudeProject{adapter: project}
		p.projects.draining = map[string]*drained{"/repo": nil}
		if idle, err := p.ReloadIdle(t.Context()); idle || err == nil {
			t.Fatalf("recovery drain ignored: %v %v", idle, err)
		}
		p.projects.draining = nil
		_ = store.Close()
		if idle, err := p.ReloadIdle(t.Context()); idle || err == nil {
			t.Fatalf("inventory error ignored: %v %v", idle, err)
		}
	}
}
