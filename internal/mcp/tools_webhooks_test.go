package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func webhookRequest(args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestWebhookToolValidation(t *testing.T) {
	for _, args := range []map[string]any{
		{}, {"action": "unknown"}, {"action": "create"}, {"action": "create", "name": " "},
		{"action": "create", "name": "test", "secret": false},
		{"action": "get"}, {"action": "get", "inbox_id": "../settings"},
		{"action": "get", "inbox_id": "id?x=y"}, {"action": "get", "inbox_id": "id%2fsubscriptions"},
		{"action": "get", "inbox_id": "id\\subscriptions"}, {"action": "get", "inbox_id": " id"},
		{"action": "get", "inbox_id": "id\x00"},
		{"action": "subscribe", "inbox_id": "inbox"},
		{"action": "subscribe", "inbox_id": "inbox", "routine_id": "routine"},
		{"action": "subscribe", "inbox_id": "inbox", "routine_id": "routine", "header_predicates": "{}"},
		{"action": "subscribe", "inbox_id": "inbox", "routine_id": "routine", "header_predicates": "{}", "json_predicates": " "},
	} {
		t.Run(string(mustJSON(t, args)), func(t *testing.T) {
			handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid input reached handler") })
			result := handleWebhookTool(t.Context(), handler, webhookRequest(args))
			if !result.IsError {
				t.Fatalf("expected error: %+v", result)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWebhookToolRoutes(t *testing.T) {
	for _, tc := range []struct {
		action, method, path, response string
		code                           int
	}{
		{"list", "GET", "/api/webhook-inboxes", `[{"id":"inbox","secret":"hidden","ingestionUrl":"/i/token"}]`, 200},
		{"create", "POST", "/api/webhook-inboxes", `{"id":"inbox","secret":"hidden"}`, 201},
		{"get", "GET", "/api/webhook-inboxes/inbox", `{"id":"inbox","secret":"hidden"}`, 200},
		{"subscribe", "PUT", "/api/webhook-inboxes/inbox/subscriptions", `{"routineId":"routine"}`, 200},
		{"unsubscribe", "DELETE", "/api/webhook-inboxes/inbox/subscriptions", "", 204},
		{"deliveries", "GET", "/api/webhook-inboxes/inbox/deliveries", `[{"body":"untrusted"}]`, 200},
	} {
		t.Run(tc.action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path || r.Context() != ctx {
					t.Fatalf("request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if tc.action == "subscribe" && (body["routineId"] != "routine" || body["headerPredicates"] != "{}" || body["jsonPredicates"] != "{}") {
					t.Fatalf("subscription body: %+v", body)
				}
				if tc.action == "create" && (body["secretHeader"] != "Authorization" || body["secret"] != "hidden") {
					t.Fatalf("create body: %+v", body)
				}
				if _, ok := body["enrollmentToken"]; ok {
					t.Fatal("unlisted argument was forwarded")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.response))
			})
			tools := webhookServerTools(handler)
			if len(tools) != 1 || tools[0].Tool.Name != "webhooks" {
				t.Fatalf("tools: %+v", tools)
			}
			result, err := tools[0].Handler(ctx, webhookRequest(map[string]any{
				"action": tc.action, "name": "test", "inbox_id": "inbox", "routine_id": "routine",
				"secret": "hidden", "secret_header": "Authorization", "enrollmentToken": "ignored",
				"header_predicates": "{}", "json_predicates": "{}",
			}))
			if err != nil || result.IsError || strings.Contains(string(mustJSON(t, result)), "hidden") {
				t.Fatalf("result: %+v, %v", result, err)
			}
		})
	}
}

func TestWebhookToolHelpAndErrors(t *testing.T) {
	if len(webhookServerTools(nil)) != 0 {
		t.Fatal("registered without handler")
	}
	result := handleWebhookTool(t.Context(), nil, webhookRequest(map[string]any{"action": "help"}))
	if result.IsError || !strings.Contains(string(mustJSON(t, result)), "json_predicates") {
		t.Fatalf("help: %+v", result)
	}
	for _, tc := range []struct {
		code int
		body string
	}{
		{400, "invalid predicates"}, {404, "not found"}, {503, "relay is not configured"}, {200, "not JSON"},
	} {
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = w.Write([]byte(tc.body))
		})
		if result := handleWebhookTool(t.Context(), handler, webhookRequest(map[string]any{"action": "list"})); !result.IsError {
			t.Fatalf("expected error for %d %s", tc.code, tc.body)
		}
	}
}
