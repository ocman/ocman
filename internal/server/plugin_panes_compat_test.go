package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
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
			return remote.PluginResponse{Error: &plugins.WireError{Category: plugins.ErrorInvalidArgument}}
		}
		switch request.Operation {
		case "available":
			return remote.PluginResponse{Value: json.RawMessage(`true`)}
		case "catalog":
			return remote.PluginResponse{Value: encodeJSON(t, legacy)}
		default:
			return remote.PluginResponse{Error: &plugins.WireError{Category: plugins.ErrorInvalidArgument}}
		}
	})
	body := hub.call(t, http.MethodGet, "/panes?ownerId=machine", "", http.StatusOK)
	var panes []json.RawMessage
	if err := json.Unmarshal([]byte(body), &panes); err != nil || len(panes) != 0 {
		t.Fatalf("legacy pane catalog = %s, %v", body, err)
	}
	disconnect()
	hub.call(t, http.MethodGet, "/panes?ownerId=machine", "", http.StatusServiceUnavailable)
}
