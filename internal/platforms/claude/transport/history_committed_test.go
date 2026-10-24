package claudecode

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor/providertest"
)

func TestCommittedChildHistoryReachesCapableSupervisor(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "capable"}[capable], func(t *testing.T) {
			socket := filepath.Join(providertest.Directory(t), "s")
			ln, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			var fail atomic.Bool
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hello":
					_ = json.NewEncoder(w).Encode(providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "claudecode", CLIVersion: "test-only", Capabilities: providerprotocol.Capabilities{HistoryConfirm: capable}})
				case "/sessions":
					_ = json.NewEncoder(w).Encode([]db.Session{{ID: "agent-child", ParentID: "root"}})
				case "/sessions/agent-child/history-confirmed":
					calls.Add(1)
					var receipt providerprotocol.HistoryConfirmation
					if json.NewDecoder(r.Body).Decode(&receipt) != nil || receipt.Entries["m:message"] != "present" {
						t.Error("lost committed message receipt")
					}
					if fail.Load() {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			})}
			go func() { _ = srv.Serve(ln) }()
			defer srv.Close()
			a, err := provider.New(t.Context(), socket)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if _, err := a.RuntimeSessions(t.Context()); err != nil {
				t.Fatal(err)
			}
			p := &claudePlatform{projects: &claudeProjects{roots: map[string]*claudeProject{"/repo": {watched: watched{adapter: a}}}}}
			snapshot := &db.HistorySnapshot{Subagent: true, Messages: []db.Message{{ID: "message"}}}
			if err := p.HistoryCommitted(t.Context(), "agent-child", snapshot); err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if capable {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("confirmation capability was not respected")
			}
			if err := p.HistoryCommitted(t.Context(), "unknown", snapshot); err != nil || calls.Load() != want {
				t.Fatal("unknown child was confirmed")
			}
			if err := p.HistoryCommitted(t.Context(), "agent-child", nil); err != nil {
				t.Fatal(err)
			}
			if capable {
				fail.Store(true)
				if err := p.HistoryCommitted(t.Context(), "agent-child", snapshot); err == nil {
					t.Fatal("confirmation failure was hidden")
				}
			}
		})
	}
}
