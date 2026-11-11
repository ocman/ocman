package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type sidebarWatchNative struct {
	*opencode.Adapter
	port string
}

func (*sidebarWatchNative) Available(context.Context) bool { return true }
func (*sidebarWatchNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: "watch-source"}, nil
}
func (*sidebarWatchNative) SessionIdentityMetadata(_ context.Context, id string) (*db.Session, error) {
	return &db.Session{ID: id, Directory: "/repo"}, nil
}
func (p *sidebarWatchNative) InstancePorts() platforms.InstancePorts { return p }
func (p *sidebarWatchNative) DiscoverOpenCodePorts() map[string]string {
	return map[string]string{"/repo": p.port}
}
func (p *sidebarWatchNative) DiscoverOpenCodePort(string) string { return p.port }

func TestConnectionWatcherKeepsRunningSidebarSessionBusy(t *testing.T) {
	for _, key := range []platforms.ID{"opencode", "cnx-account"} {
		t.Run(string(key), func(t *testing.T) { testSidebarWatcherLifecycle(t, key) })
	}
}

func testSidebarWatcherLifecycle(t *testing.T, connection platforms.ID) {
	t.Helper()
	core := statetest.Open(t, ModuleSchemas()...)
	history := statev2test.Open(t)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	statusUpdates := make(chan string)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/info":
			http.NotFound(w, r)
		case "/global/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"native\",\"status\":{\"type\":\"busy\"}}}\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case status := <-statusUpdates:
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"native\",\"status\":{\"type\":\"%s\"}}}\n\n", status)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case "/session/status":
			_, _ = fmt.Fprint(w, `{"native":{"type":"busy"}}`)
		default:
			_, _ = fmt.Fprint(w, `[]`)
		}
	}))
	t.Cleanup(upstream.Close)
	native := &sidebarWatchNative{Adapter: opencode.New(nil, nil, nil, ocapi.Auth{}), port: strings.TrimPrefix(upstream.URL, "http://127.0.0.1:")}
	adapter, err := owned.New(t.Context(), native, catalogue, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "watch-source"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	identity, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "watch-source", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: "opencode", SourceKey: "watch-source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Commit(t.Context(), statev2.Batch{
		Session:  statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: identity.ID}, Directory: "/repo", Title: "running", ReadFormat: statev2.ConversationReadFormat},
		Messages: []statev2.Message{{ID: "assistant", Role: "assistant", Finish: "tool-calls", ConversationVisible: true, Extensions: json.RawMessage(`{"role":"assistant","finish":"tool-calls"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	if connection == "opencode" {
		registry.Register(adapter)
	} else {
		if err := registry.RegisterConnection(connection, adapter); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(nil, core, "", registry, nil)
	ctx, cancel := context.WithCancel(t.Context())
	watcher := srv.autoapproveModule()
	sidebar := adapter.SidebarModule()
	for _, worker := range []module.Module{watcher, sidebar} {
		if err := worker.(module.Starter).Start(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
	}
	watchDone, sidebarDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(watchDone); watcher.Run(ctx) }()
	go func() { defer close(sidebarDone); sidebar.(module.Runner).Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-watchDone
		<-sidebarDone
		sidebar.(module.Finalizer).Finalize()
	})
	expectStatus := func(want db.SessionStatus) {
		t.Helper()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			rows, err := adapter.SidebarSessions(ctx, platforms.SidebarQuery{})
			if err != nil || len(rows) != 1 {
				t.Fatalf("sidebar rows: %+v, %v", rows, err)
			}
			if rows[0].Status == want {
				return
			}
			select {
			case <-deadline.C:
				t.Fatalf("connection session appears %s in sidebar, want %s", rows[0].Status, want)
			case <-tick.C:
			}
		}
	}
	expectStatus(db.StatusBusy)
	statusUpdates <- "idle"
	expectStatus(db.StatusDone)
	statusUpdates <- "busy"
	expectStatus(db.StatusBusy)
}
