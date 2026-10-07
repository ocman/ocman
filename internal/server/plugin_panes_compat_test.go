package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestPaneDiscoveryAcceptsLegacyOwnerCatalog(t *testing.T) {
	hub := newPluginManagementTest(t)
	legacy := []state.PluginRegistration{{
		Description: plugins.Description{ID: "org.example.legacy", Name: "Legacy", Version: "1", Scope: plugins.ScopeOwner, Protocol: plugins.Version{Major: 1}, MaxConcurrency: 1, Capabilities: []plugins.Capability{plugins.ActionCapability}},
		Enabled:     true, Grants: []string{"context.owner"},
	}}
	disconnect := connectPluginOwnerHandler(t, hub.s, func(_ context.Context, request remote.PluginRequest) remote.PluginResponse {
		// Previous protocol-7 owners reject both unknown envelope fields and
		// new operations, but still support their existing plugin catalog.
		if request.Pane != (plugins.PaneRequest{}) {
			return pluginResponse(nil, plugins.ErrInvalidMessage)
		}
		switch request.Operation {
		case "available":
			return pluginResponse(true, nil)
		case "catalog":
			return pluginResponse(legacy, nil)
		default:
			return pluginResponse(nil, plugins.ErrInvalidMessage)
		}
	})
	body := hub.call(t, http.MethodGet, "/panes?ownerId=machine", "", http.StatusOK)
	var panes []listedPluginPane
	if err := json.Unmarshal([]byte(body), &panes); err != nil || len(panes) != 0 {
		t.Fatalf("legacy pane catalog = %s, %v", body, err)
	}
	disconnect()
	hub.call(t, http.MethodGet, "/panes?ownerId=machine", "", http.StatusServiceUnavailable)
}
