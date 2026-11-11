package autoapprove

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

type scopedWatcherPlatform struct {
	*platformtest.Fake
	port string
}

func (p *scopedWatcherPlatform) InstancePorts() platforms.InstancePorts { return p }
func (p *scopedWatcherPlatform) DiscoverOpenCodePorts() map[string]string {
	return map[string]string{"/repo": p.port}
}
func (p *scopedWatcherPlatform) DiscoverOpenCodePort(string) string { return p.port }

func TestConnectionWatchersUseOwnTransportAndRoutingKey(t *testing.T) {
	type event struct{ platform, session string }
	events := make(chan event, 4)
	configs := make([]WatcherConfig, 0, 2)
	for _, account := range []string{"work", "personal"} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, password, _ := r.BasicAuth()
			if password != account {
				t.Error("watcher borrowed another account's authentication")
				http.Error(w, "wrong account", http.StatusUnauthorized)
				return
			}
			if r.URL.Path == "/api/info" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"ocman.queue.changed\",\"properties\":{\"sessionID\":\"same-native-id\"}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		t.Cleanup(upstream.Close)
		adapter := &scopedWatcherPlatform{Fake: &platformtest.Fake{PlatformID: "opencode"}, port: strings.TrimPrefix(upstream.URL, "http://127.0.0.1:")}
		configs = append(configs, WatcherConfig{
			Platform: platforms.ID("cnx-" + account),
			Client:   &http.Client{Transport: ocapi.New(account).Transport(http.DefaultTransport)},
			Engine: Deps{
				OpencodePlatform: func() platforms.Platform { return adapter },
				BroadcastQueueChanged: func(platform, session string) {
					events <- event{platform, session}
				},
			},
		})
	}
	m := NewModule(ModuleDeps{Watchers: func() []WatcherConfig { return configs }})
	ctx, cancel := context.WithCancel(t.Context())
	if err := m.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("connection watchers outlived module shutdown")
		}
	})
	seen := map[string]bool{}
	for len(seen) < len(configs) {
		select {
		case got := <-events:
			if got.session != "same-native-id" || got.platform != "cnx-work" && got.platform != "cnx-personal" {
				t.Fatalf("watcher lost account identity: %+v", got)
			}
			seen[got.platform] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("missing account events: %v", seen)
		}
	}
}

func TestConnectionWatcherKeepsNativeCommitStorageScope(t *testing.T) {
	store := &commitStoreStub{}
	adapter := &platformtest.Fake{PlatformID: "opencode"}
	w := newAutoApproveWatcher(NewService(Deps{}))
	w.scope = &WatcherConfig{Platform: "cnx-work", Engine: Deps{
		CommitStore:      store,
		OpencodePlatform: func() platforms.Platform { return adapter },
	}}
	w.recordTerminalPart(t.Context(), terminalPart{SessionID: "canonical", PartID: "part", Command: "git commit -m captured", Output: "[main abc1234] captured"})
	if len(store.commits) != 1 || store.commits[0].Platform != "opencode" || store.commits[0].SessionID != "canonical" {
		t.Fatalf("commit storage used connection routing identity: %+v", store.commits)
	}
}
