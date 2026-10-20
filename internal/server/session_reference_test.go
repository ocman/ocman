package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/httpx"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestSessionReferenceOwnerPolicyMatchesAcrossTransports(t *testing.T) {
	for _, tc := range []struct {
		name, platform, remote string
		reject                 bool
	}{
		{"infer owner", "", "", false},
		{"explicit platform", "test", "", false},
		{"explicit local owner", "", "local", false},
		{"wrong platform", "missing", "", true},
		{"wrong remote owner", "", "other-owner", true},
		{"disconnected explicit owner", "test", "other-owner", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, transport := range []string{"REST", "MCP"} {
				t.Run(transport, func(t *testing.T) {
					s, _, ownedID := identityHTTPFixture(t)
					called := false
					check := func(platform, id string) {
						called = true
						if platform != "test" || id != "native" {
							t.Fatalf("wrong native target: %s %s", platform, id)
						}
					}
					if transport == "REST" {
						body, err := json.Marshal(map[string]string{"platform": tc.platform, "remoteId": tc.remote, "sessionId": ownedID})
						if err != nil {
							t.Fatal(err)
						}
						w := httptest.NewRecorder()
						s.withSessionViews(func(w http.ResponseWriter, r *http.Request) {
							var req struct {
								Platform  string `json:"platform"`
								RemoteID  string `json:"remoteId"`
								SessionID string `json:"sessionId"`
							}
							if httpx.ReadJSON(w, r, 4096, &req) {
								check(req.Platform, req.SessionID)
							}
						})(w, httptest.NewRequest(http.MethodPost, "/api/routines", strings.NewReader(string(body))))
						if tc.reject && w.Code < 400 {
							t.Fatalf("invalid owner accepted: %d", w.Code)
						}
					} else {
						req := mcplib.CallToolRequest{}
						req.Params.Name, req.Params.Arguments = "routines", map[string]any{"action": "create", "platform": tc.platform, "remote_id": tc.remote, "session_id": ownedID}
						result, err := s.sessionIdentityToolMiddleware(func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
							check(req.GetString("platform", ""), req.GetString("session_id", ""))
							return mcplib.NewToolResultText("ok"), nil
						})(t.Context(), req)
						if err != nil || result.IsError != tc.reject {
							t.Fatalf("unexpected tool result: %+v %v", result, err)
						}
					}
					if called == tc.reject {
						t.Fatalf("native handler called=%v, reject=%v", called, tc.reject)
					}
				})
			}
		})
	}
}

func TestInheritedParentReferenceDoesNotChangeCreationTarget(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	scope := newSessionReferenceScope("target-platform", "local")
	id, err := scope.resolve(t.Context(), s, ownedID, true)
	if err != nil || id != "native" || scope.platform != "target-platform" {
		t.Fatalf("inheritance changed target: %s %+v %v", id, scope, err)
	}
}

func TestNativeReferencePassesThroughWithoutIdentityLookup(t *testing.T) {
	scope := newSessionReferenceScope("test", "local")
	for _, id := range []string{"", "native"} {
		nativeID, err := scope.resolve(t.Context(), nil, id, false)
		if err != nil || nativeID != id || scope.platform != "test" {
			t.Fatalf("native reference changed: %s %v", nativeID, err)
		}
	}
}
