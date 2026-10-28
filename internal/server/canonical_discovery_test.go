package server

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type discoverySourcePlatform struct{ *opencode.Adapter }

func (*discoverySourcePlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: "test-source"}, nil
}

func TestHostDiscoveryHooksSurviveCanonicalAdapterWrapping(t *testing.T) {
	core := statetest.Open(t, sessioncatalog.Schema)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	native := &discoverySourcePlatform{Adapter: opencode.New(nil, nil, nil, ocapi.Auth{})}
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(adapter)
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	srv := &Server{registry: registry}
	srv.setOpenCodeMachineServer("4600")
	srv.openCodeInventoryChanged()
	if got := native.Discovery().MachineServerPort(); got != "4600" {
		t.Fatalf("canonical wrapping detached Host runtime hooks: %q", got)
	}
}
