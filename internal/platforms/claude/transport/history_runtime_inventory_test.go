package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// A supervisor retains terminal status observations for helpers whose files
// may be gone. Those observations are not authority to import a transcript.
func TestHistoryInventoryExcludesStatusOnlyHelpersButKeepsRuntimeBootstrap(t *testing.T) {
	root := testutil.ShortTempDir(t, "history-inventory-")
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
	runtime := db.Session{ID: "runtime-root", Directory: "/repo", Title: "new session", Status: db.StatusBusy}
	socket := filepath.Join(root, "s")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/hello":
			_ = json.NewEncoder(w).Encode(providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "claudecode", CLIVersion: "test-only"})
		case "/sessions":
			if r.URL.Query().Get("live") != "" {
				_ = json.NewEncoder(w).Encode([]db.Session{runtime, {ID: "agent-old-helper", Status: db.StatusDone}})
			} else {
				_ = json.NewEncoder(w).Encode([]db.Session{runtime})
			}
		case "/sessions/runtime-root":
			_ = json.NewEncoder(w).Encode(platforms.SessionDetail{Session: &runtime})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	go func() { _ = server.Serve(ln) }()
	defer server.Close()
	adapter, err := provider.New(t.Context(), socket)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	p := &claudePlatform{history: history, configDir: config, projects: &claudeProjects{
		roots: map[string]*claudeProject{"/repo": {watched: watched{adapter: adapter}}},
	}}
	entries, err := p.HistoryInventory(t.Context())
	if err != nil || len(entries) != 1 || entries[0].ID != runtime.ID {
		t.Fatalf("status-only helper became an unreadable transcript: %+v %v", entries, err)
	}
	snapshot, err := p.HistorySnapshot(t.Context(), runtime.ID)
	if err != nil || !snapshot.Partial || snapshot.Session.ID != runtime.ID || snapshot.Session.Directory != "/repo" {
		t.Fatalf("real pre-transcript runtime was lost: %+v %v", snapshot, err)
	}
	failed, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unreachable", http.StatusServiceUnavailable)
	})
	p.projects.roots["/failed"] = &claudeProject{watched: watched{adapter: failed}}
	entries, err = p.HistoryInventory(t.Context())
	var incomplete *platforms.HistoryDeferred
	if len(entries) != 1 || entries[0].ID != runtime.ID || !errors.As(err, &incomplete) || incomplete.Count != 1 {
		t.Fatalf("one failed supervisor blocked healthy history: %+v %v", entries, err)
	}
	stalled, _ := reloadTestAdapter(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	p.projects.roots["/stalled"] = &claudeProject{watched: watched{adapter: stalled}}
	probeCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	entries, err = p.HistoryInventory(probeCtx)
	if len(entries) != 1 || !errors.As(err, &incomplete) || incomplete.Count != 2 || probeCtx.Err() != nil {
		t.Fatalf("stalled supervisor consumed transcript inventory deadline: %+v %v parent=%v", entries, err, probeCtx.Err())
	}
}
