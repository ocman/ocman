package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	claudeprovider "github.com/ocman/ocman/internal/platforms/claude/provider"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
)

func TestRuntimeCleanupProbesAreSupportedAndFailClosedWithoutHistory(t *testing.T) {
	if _, ok := any(&claudePlatform{}).(platforms.RuntimeSessionReader); ok {
		t.Fatal("strict cleanup inventory must not replace fresh-install migration reads")
	}
	reader, ok := any(&claudePlatform{}).(platforms.WorktreeRuntimeReader)
	if !ok {
		t.Fatal("default Claude integration cannot provide cleanup safety inventory")
	}
	if _, err := reader.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("missing source accepted as empty")
	}
	if _, err := reader.WorktreeRuntimeLifecycle(t.Context(), "missing"); err == nil {
		t.Fatal("missing source accepted as an idle session")
	}
}

func TestRuntimeCleanupClaudeRejectsRuntimeDirectoryDisagreement(t *testing.T) {
	for _, runtimeDir := range []string{"/repo", "/elsewhere"} {
		t.Run(runtimeDir, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config")
			project := filepath.Join(config, "projects", "-elsewhere")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			body := `{"type":"assistant","uuid":"line","sessionId":"root","cwd":"/elsewhere","timestamp":"2026-01-01T00:00:01Z","message":{"id":"msg","model":"test","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}` + "\n"
			if err := os.WriteFile(filepath.Join(project, "root.jsonl"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var history *claudeprovider.HistoryReader
			opts := claudeprovider.DefaultOptions(root)
			opts.ConfigDir, opts.CLI = config, claudetest.Binary(t)
			opts.HistoryReady = func(h *claudeprovider.HistoryReader) { history = h }
			if _, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(root, "spool")); err != nil {
				t.Fatal(err)
			}
			adapter, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode([]db.Session{{ID: "root", Directory: runtimeDir, Status: db.StatusBusy}})
			})
			p := &claudePlatform{Adapter: adapter, history: history, configDir: config, projects: &claudeProjects{roots: map[string]*claudeProject{"/repo": {watched: watched{adapter: adapter}}}}}
			rows, err := p.WorktreeRuntimeSessions(t.Context())
			if runtimeDir != "/elsewhere" {
				if err == nil {
					t.Fatal("transcript outside checkout hid a runtime using the checkout")
				}
				return
			}
			if err != nil || len(rows) != 1 {
				t.Fatalf("matching native inventory: %+v %v", rows, err)
			}
			if life, err := p.WorktreeRuntimeLifecycle(t.Context(), "root"); err != nil || life.Status != db.StatusBusy || life.Directory != runtimeDir {
				t.Fatalf("matching native lifecycle: %+v %v", life, err)
			}
		})
	}
}

func TestRuntimeCleanupClaudeUnavailableSupervisorFailsClosed(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(config, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var history *claudeprovider.HistoryReader
	opts := claudeprovider.DefaultOptions(root)
	opts.ConfigDir, opts.CLI = config, claudetest.Binary(t)
	opts.HistoryReady = func(h *claudeprovider.HistoryReader) { history = h }
	if _, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(root, "spool")); err != nil {
		t.Fatal(err)
	}
	adapter, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions" {
			http.Error(w, "supervisor unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{})
	})
	p := &claudePlatform{history: history, configDir: config, projects: &claudeProjects{roots: map[string]*claudeProject{"/repo": {watched: watched{adapter: adapter}}}}}
	if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("failed runtime inventory accepted as empty")
	}
	if _, err := p.WorktreeRuntimeLifecycle(t.Context(), "child"); err == nil {
		t.Fatal("failed pre-transcript identity accepted")
	}
}
