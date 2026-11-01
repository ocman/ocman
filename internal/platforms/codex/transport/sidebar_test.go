package codextransport

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor"
)

func TestCodexSidebarUsesCachedNativeChildObservations(t *testing.T) {
	p := &Platform{}
	observer, ok := any(p).(platforms.CachedSessionObserver)
	if !ok {
		t.Fatal("Codex does not expose live child observations to the canonical sidebar")
	}
	availability, ok := any(p).(platforms.CachedSessionAvailability)
	if !ok {
		t.Fatal("Codex does not expose cached connection availability")
	}
	if len(observer.CachedSessionObservations()) != 0 || availability.CachedSessionAvailable() {
		t.Fatal("unstarted supervisor appears live")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir, err := os.MkdirTemp("", "cx-") // short unix socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sup, err := providersupervisor.New(filepath.Join(dir, "spool"), providersupervisor.Backend{Hello: providerprotocol.Hello{
		ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "codex", CLIVersion: "fixture", Capabilities: providerprotocol.Capabilities{Capabilities: platforms.Capabilities{Events: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: sup, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	adapter, err := provider.New(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adapter.Close)
	p.adapter = adapter
	seen := make(chan struct{}, 10)
	adapter.SetEventHandler(func(providerprotocol.Event) { seen <- struct{}{} })
	go func() { _ = adapter.WatchEvents(ctx) }()
	for _, status := range []string{"busy", "done"} {
		props, _ := json.Marshal(map[string]any{"sessionID": "native-child", "status": map[string]any{"type": status}})
		if err := sup.Publish(providerprotocol.Event{Type: "session.status", Properties: props}, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case <-seen:
		case <-time.After(5 * time.Second):
			t.Fatal("child observation was not cached")
		}
		if got := observer.CachedSessionObservations()["native-child"]; got.Status != status {
			t.Fatalf("child observation = %+v", got)
		}
		if !availability.CachedSessionAvailable() {
			t.Fatal("connected supervisor appears offline")
		}
	}
	adapter.Close()
	if availability.CachedSessionAvailable() || len(observer.CachedSessionObservations()) != 0 {
		t.Fatal("retired supervisor kept live child observations")
	}
}
