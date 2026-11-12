package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state/statetest"
)

type usageAccountPlatform struct {
	*platformtest.Fake
	percent float64
	calls   int
}

func (p *usageAccountPlatform) Account(context.Context) (*providerprotocol.Account, error) {
	p.calls++
	return &providerprotocol.Account{SubscriptionType: "max", RateLimits: []providerprotocol.RateLimit{{Name: "weekly", Utilization: p.percent}}}, nil
}

func TestProviderUsageNamedConnections(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	var connections []platformconnections.Connection
	var adapters []*usageAccountPlatform
	for i, name := range []string{"Claude Work", "Claude Personal"} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: name, Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		p := &usageAccountPlatform{Fake: &platformtest.Fake{PlatformID: "claudecode"}, percent: float64(i+1) / 4}
		if err := registry.RegisterConnection(platforms.ID(c.ID), p); err != nil {
			t.Fatal(err)
		}
		connections, adapters = append(connections, c), append(adapters, p)
	}
	s := New(nil, core, "127.0.0.1:0", registry, nil)
	s.connectionStore = store
	s.connections = platformconnections.NewRouter(store)
	read := func() []struct {
		ID, Name string
		Windows  []struct{ UsedPercent float64 }
	} {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/provider-usage", nil)
		request.RemoteAddr = "127.0.0.1:1234"
		serveModule(s, s.analyticsModule(), response, request)
		var result struct {
			Providers []struct {
				ID, Name string
				Windows  []struct{ UsedPercent float64 }
			}
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("usage = %d %s", response.Code, response.Body.String())
		}
		return result.Providers
	}
	got := read()
	if len(got) != 2 || got[0].ID != connections[0].ID || got[0].Name != "Claude Work" || got[1].ID != connections[1].ID || got[1].Name != "Claude Personal" || got[0].Windows[0].UsedPercent != 25 || got[1].Windows[0].UsedPercent != 50 {
		t.Fatalf("account identity lost: %+v", got)
	}
	if err := store.Rename(t.Context(), connections[0].ID, "Claude Office"); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got[0].ID != connections[0].ID || got[0].Name != "Claude Office" || adapters[0].calls != 1 || adapters[1].calls != 1 {
		t.Fatalf("rename invalidated account identity or cached quota: %+v, calls %d/%d", got, adapters[0].calls, adapters[1].calls)
	}
	if err := store.SetEnabled(t.Context(), connections[0].ID, false); err != nil {
		t.Fatal(err)
	}
	got = read()
	if len(got) != 1 || got[0].ID != connections[1].ID {
		t.Fatalf("disabled account still shown: %+v", got)
	}
	if err := store.Remove(t.Context(), connections[1].ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("removed account still shown: %+v", got)
	}
}

func TestSubscriptionSourcesLegacyAndInventoryFailure(t *testing.T) {
	if got, err := (&Server{}).subscriptionSources(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("absent registry = %+v, %v", got, err)
	}
	registry := platforms.NewRegistry()
	local := &usageAccountPlatform{Fake: &platformtest.Fake{PlatformID: "codex"}}
	registry.Register(local)
	remote := &usageAccountPlatform{Fake: &platformtest.Fake{PlatformID: "codex"}}
	if err := registry.RegisterConnection("r-other:cnx_codex", remote); err != nil {
		t.Fatal(err)
	}
	s := &Server{registry: registry}
	got, err := s.subscriptionSources(t.Context())
	if err != nil || len(got) != 1 || got[0].Key != "codex" || got[0].Platform != local || remote.calls != 0 {
		t.Fatalf("legacy owner sources = %+v, %v", got, err)
	}
	core := statetest.Open(t, ModuleSchemas()...)
	s.connectionStore = platformconnections.New(core)
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.subscriptionSources(t.Context()); err == nil || len(got) != 0 {
		t.Fatalf("failed inventory borrowed legacy account: %+v, %v", got, err)
	}
}
