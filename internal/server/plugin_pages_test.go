package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestPluginPagesFollowOwnerAndGrants(t *testing.T) {
	hub := newPluginManagementTest(t)
	d := plugins.Description{ID: "org.example.pages", Name: "Pages", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1, Capabilities: []plugins.Capability{plugins.PageCapability}, RequestedGrants: []string{plugins.PageProjectGrant}, Pages: []plugins.PageDescriptor{{ID: "pentest", Label: "Pentest"}}}
	enabled, granted := true, true
	calls := 0
	disconnect := connectPluginOwnerHandler(t, hub.s, func(_ context.Context, r remote.PluginRequest) remote.PluginResponse {
		switch r.Operation {
		case "available":
			return remote.PluginResponse{Value: json.RawMessage(`true`)}
		case "catalog":
			grants := []string{}
			if granted {
				grants = []string{plugins.PageProjectGrant}
			}
			return remote.PluginResponse{Value: encodeJSON(t, []state.PluginRegistration{{Description: d, Enabled: enabled, Grants: grants}})}
		case "page-read", "page-save":
			calls++
			var req plugins.PageRequest
			if r.PluginID != d.ID || json.Unmarshal(r.Input.Values["page"], &req) != nil || req.ProjectID != "project-a" {
				t.Error("owner request changed")
			}
			return remote.PluginResponse{Value: encodeJSON(t, plugins.PageDocument{Values: json.RawMessage(`{}`)})}
		default:
			return remote.PluginResponse{Error: &plugins.WireError{Category: plugins.ErrorInvalidArgument}}
		}
	})
	body := hub.call(t, http.MethodGet, "/pages?ownerId=machine", "", http.StatusOK)
	if !strings.Contains(body, `"ownerId":"machine"`) || !strings.Contains(body, "Pentest") || calls != 0 {
		t.Fatalf("page catalog: %s", body)
	}
	granted = false
	if got := hub.call(t, http.MethodGet, "/pages?ownerId=machine", "", http.StatusOK); strings.TrimSpace(got) != "[]" {
		t.Fatal("revoked page listed")
	}
	granted = true
	enabled = false
	if got := hub.call(t, http.MethodGet, "/pages?ownerId=machine", "", http.StatusOK); strings.TrimSpace(got) != "[]" {
		t.Fatal("disabled page listed")
	}
	for _, method := range []string{"read", "save"} {
		hub.call(t, http.MethodPost, "/pages/call", `{"ownerId":"machine","pluginId":"org.example.pages","method":"`+method+`","request":{"pageId":"pentest","projectId":"project-a"}}`, http.StatusOK)
	}
	if calls != 2 {
		t.Fatal("page operation not routed")
	}
	disconnect()
	hub.call(t, http.MethodPost, "/pages/call", `{"ownerId":"machine","pluginId":"org.example.pages","method":"read","request":{"pageId":"pentest","projectId":"project-a"}}`, http.StatusServiceUnavailable)
	if calls != 2 {
		t.Fatal("offline owner fell back")
	}
}
