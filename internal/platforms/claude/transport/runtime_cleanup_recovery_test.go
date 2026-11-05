package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	hostlocal "github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/ocruntime"
	claudeprovider "github.com/ocman/ocman/internal/platforms/claude/provider"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func cleanupRecoveringClaude(t *testing.T) *claudePlatform {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(config, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	var history *claudeprovider.HistoryReader
	opts := claudeprovider.DefaultOptions(root)
	opts.ConfigDir, opts.CLI = config, claudetest.Binary(t)
	opts.HistoryReady = func(h *claudeprovider.HistoryReader) { history = h }
	if _, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(root, "spool")); err != nil {
		t.Fatal(err)
	}
	return &claudePlatform{history: history, configDir: config, projects: &claudeProjects{roots: map[string]*claudeProject{}, mgr: &hostlocal.ProviderManager{State: statetest.Open(t), Runtime: ocruntime.NewBackendRuntime("claudecode", "", "")}}}
}

func TestCleanupIncludesPersistedUnattachedClaudeSupervisor(t *testing.T) {
	p := cleanupRecoveringClaude(t)
	_, socket := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]db.Session{{ID: "unindexed", Directory: "/repo", Status: db.StatusBusy}})
	})
	if err := p.projects.mgr.State.UpsertManagedProvider(t.Context(), "claudecode", "/repo", state.ManagedInstance{Endpoint: "unix://" + socket}); err != nil {
		t.Fatal(err)
	}
	rows, err := p.WorktreeRuntimeSessions(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != "unindexed" {
		t.Fatalf("persisted supervisor omitted: %+v %v", rows, err)
	}
	if len(p.projects.roots) != 0 {
		t.Fatal("cleanup adopted execution supervisor")
	}
}

func TestCleanupRejectsUnreadableOrDrainingClaudeSupervisor(t *testing.T) {
	t.Run("unreadable", func(t *testing.T) {
		p := cleanupRecoveringClaude(t)
		if err := p.projects.mgr.State.UpsertManagedProvider(t.Context(), "claudecode", "/repo", state.ManagedInstance{Endpoint: "unix://missing"}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
			t.Fatal("unresolved persisted supervisor accepted")
		}
	})
	t.Run("draining", func(t *testing.T) {
		p := cleanupRecoveringClaude(t)
		p.projects.draining = map[string]*drained{"/repo": nil}
		if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
			t.Fatal("draining supervisor accepted")
		}
	})
}
