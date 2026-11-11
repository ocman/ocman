package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
)

type watcherClientPlatform struct {
	*fakePlatform
	observer platforms.OpenCodeEventObserver
	client   *http.Client
}

func (p *watcherClientPlatform) OpenCodeObserver() platforms.OpenCodeEventObserver {
	return p.observer
}
func (p *watcherClientPlatform) OpenCodeEventClient() *http.Client { return p.client }

func TestOpenCodeWatchersKeepLocalConnectionScopes(t *testing.T) {
	registry := platforms.NewRegistry()
	srv := &Server{registry: registry}
	for _, key := range []platforms.ID{"cnx-work", "cnx-personal"} {
		adapter := &watcherClientPlatform{
			fakePlatform: &fakePlatform{PlatformID: "opencode"},
			observer:     &opencode.Adapter{},
			client:       &http.Client{},
		}
		if err := registry.RegisterConnection(key, adapter); err != nil {
			t.Fatal(err)
		}
	}
	for _, adapter := range []platforms.Platform{
		&fakePlatform{PlatformID: "claudecode"},
		&fakePlatform{PlatformID: "r-other:opencode"},
		&fakePlatform{PlatformID: "opencode"},
		&watcherClientPlatform{fakePlatform: &fakePlatform{PlatformID: "opencode"}, observer: &opencode.Adapter{}},
		&watcherClientPlatform{fakePlatform: &fakePlatform{PlatformID: "opencode"}, client: &http.Client{}},
	} {
		if err := registry.RegisterConnection(platforms.ID("cnx-skip-"+string(rune('a'+len(registry.Entries())))), adapter); err != nil {
			t.Fatal(err)
		}
	}
	configs := srv.openCodeWatchers()
	if len(configs) != 2 {
		t.Fatalf("unexpected watcher scopes: %+v", configs)
	}
	for _, config := range configs {
		adapter, _ := registry.Get(config.Platform)
		if config.Engine.OpencodePlatform() != adapter || config.Client != adapter.(*watcherClientPlatform).client {
			t.Fatal("watcher borrowed another connection's adapter or transport")
		}
		if err := config.Engine.RefreshSession(t.Context(), "missing"); !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("missing refresher: %v", err)
		}
	}
	if (&Server{}).openCodeWatchers() != nil {
		t.Fatal("server without registry created watchers")
	}
}

func TestLegacyOpenCodePlatformUsesOnlyDurableLegacyBinding(t *testing.T) {
	registry := platforms.NewRegistry()
	adapter := &fakePlatform{PlatformID: "opencode"}
	if err := registry.RegisterConnection("cnx-account", adapter); err != nil {
		t.Fatal(err)
	}
	srv := &Server{registry: registry}
	if srv.legacyOpenCodePlatform() != nil {
		t.Fatal("legacy lookup selected an arbitrary named account")
	}
	registry.SetLegacyResolver(func(context.Context, string) (platforms.ID, error) { return "cnx-account", nil })
	if srv.legacyOpenCodePlatform() != adapter {
		t.Fatal("lost migrated legacy binding")
	}
	registry.SetLegacyResolver(func(context.Context, string) (platforms.ID, error) { return "", platforms.ErrPlatformUnreachable })
	if srv.legacyOpenCodePlatform() != nil || (&Server{}).legacyOpenCodePlatform() != nil {
		t.Fatal("unavailable legacy owner fell back to another account")
	}
}
