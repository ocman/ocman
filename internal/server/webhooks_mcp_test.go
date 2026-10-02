package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/webhook"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func callWebhookMCP(t *testing.T, handler http.Handler, args map[string]any) mcplib.CallToolResult {
	t.Helper()
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "webhooks", "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(data)))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var response struct {
		Result mcplib.CallToolResult `json:"result"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Result.Content) == 0 {
		t.Fatalf("MCP response: %d %s", rec.Code, rec.Body.String())
	}
	return response.Result
}

func TestWebhookMCPLifecycle(t *testing.T) {
	srv, rest, _ := routineHTTPServer(t)
	srv.relayURL = fakeInboxRelay(t).URL
	routine := createTestRoutine(t, rest)
	mcp := srv.mcpHandler()
	call := func(args map[string]any) mcplib.CallToolResult {
		t.Helper()
		result := callWebhookMCP(t, mcp, args)
		if result.IsError {
			t.Fatalf("%v: %+v", args, result)
		}
		return result
	}
	call(map[string]any{"action": "list"})
	created := call(map[string]any{"action": "create", "name": "GitHub PRs", "secret": "private-secret", "secret_header": "Authorization"})
	encoded, _ := json.Marshal(created)
	for _, secret := range []string{"private-secret", "managementToken", "fetchToken", "acknowledgmentToken", "AGE-SECRET"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("leaked %s: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "/i/inbox/token") {
		t.Fatalf("missing ingestion URL: %s", encoded)
	}
	stored, err := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox")
	if err != nil || stored.Secret != "private-secret" || stored.Name != "GitHub PRs" {
		t.Fatalf("stored: %+v, %v", stored, err)
	}
	const headers = `{"X-GitHub-Event":{"equals":"pull_request"}}`
	const predicates = `{"/repository/owner/login":{"equals":"aspect-analytics"},"/pull_request/user/login":{"equals":"NoUseFreak"},"/pull_request/draft":{"equals":false},"/action":{"oneOf":["opened","synchronize","reopened","ready_for_review"]}}`
	subscribe := map[string]any{"action": "subscribe", "inbox_id": "inbox", "routine_id": routine.ID, "header_predicates": headers, "json_predicates": predicates}
	call(subscribe)
	call(subscribe) // Upsert the same pair, never duplicate it.
	subs, err := srv.stateDB.ListWebhookSubscriptions(t.Context(), "inbox")
	if err != nil || len(subs) != 1 || subs[0].HeaderPredicatesJSON != headers || subs[0].JSONPredicatesJSON != predicates {
		t.Fatalf("subscriptions: %+v, %v", subs, err)
	}
	// Exercise the actual dispatcher with predicates saved through MCP.
	routine.Enabled = true
	if err := srv.stateDB.UpdateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	dispatcher := &webhookMCPDispatcher{}
	for _, tc := range []struct {
		name, event, owner, author, action string
		draft, match                       bool
	}{
		{"own-open", "pull_request", "aspect-analytics", "NoUseFreak", "opened", false, true},
		{"own-push", "pull_request", "aspect-analytics", "NoUseFreak", "synchronize", false, true},
		{"other-author", "pull_request", "aspect-analytics", "someone-else", "opened", false, false},
		{"other-org", "pull_request", "other-org", "NoUseFreak", "opened", false, false},
		{"draft", "pull_request", "aspect-analytics", "NoUseFreak", "opened", true, false},
		{"closed", "pull_request", "aspect-analytics", "NoUseFreak", "closed", false, false},
		{"other-event", "issues", "aspect-analytics", "NoUseFreak", "opened", false, false},
	} {
		e := relay.InboxEnvelope{InboxID: "inbox", DeliveryID: tc.name}
		e.Request.Header = http.Header{"X-GitHub-Event": {tc.event}}
		e.Body, err = json.Marshal(map[string]any{
			"repository":   map[string]any{"owner": map[string]string{"login": tc.owner}},
			"pull_request": map[string]any{"user": map[string]string{"login": tc.author}, "draft": tc.draft},
			"action":       tc.action, "sender": map[string]string{"login": "NoUseFreak"},
		})
		if err != nil {
			t.Fatal(err)
		}
		before := dispatcher.calls
		if err := webhook.Dispatch(srv.stateDB, dispatcher, "inbox", tc.name, e, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := dispatcher.calls > before; got != tc.match {
			t.Fatalf("%s matched=%v, want %v", tc.name, got, tc.match)
		}
	}
	call(map[string]any{"action": "get", "inbox_id": "inbox"})
	call(map[string]any{"action": "list"})
	if _, err := srv.stateDB.AcceptWebhookDelivery(t.Context(), "inbox", "delivery", "POST", `{"action":"opened"}`, `{}`, "", 1); err != nil {
		t.Fatal(err)
	}
	call(map[string]any{"action": "deliveries", "inbox_id": "inbox"})
	call(map[string]any{"action": "unsubscribe", "inbox_id": "inbox", "routine_id": routine.ID})
	if subs, err := srv.stateDB.ListWebhookSubscriptions(t.Context(), "inbox"); err != nil || len(subs) != 0 {
		t.Fatalf("unsubscribe: %+v, %v", subs, err)
	}
}

type webhookMCPDispatcher struct{ calls int }

func (d *webhookMCPDispatcher) RunWebhook(context.Context, string, string, int64) (state.RoutineRun, error) {
	d.calls++
	return state.RoutineRun{State: "running"}, nil
}

func TestWebhookMCPDomainErrors(t *testing.T) {
	srv, rest, _ := routineHTTPServer(t)
	routine := createTestRoutine(t, rest)
	mcp := srv.mcpHandler()
	result := callWebhookMCP(t, mcp, map[string]any{"action": "create", "name": "no relay"})
	if !result.IsError {
		t.Fatal("expected missing relay error")
	}
	if err := srv.stateDB.SaveWebhookInbox(t.Context(), state.WebhookInbox{ID: "inbox", RoutineID: "inbox", RelayURL: "https://relay.invalid", Identity: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ inbox, routine, headers, predicates string }{
		{"missing", routine.ID, "{}", "{}"},
		{"inbox", "missing", "{}", "{}"},
		{"inbox", routine.ID, "[]", "{}"},
		{"inbox", routine.ID, "{}", "not json"},
	} {
		result := callWebhookMCP(t, mcp, map[string]any{"action": "subscribe", "inbox_id": tc.inbox, "routine_id": tc.routine, "header_predicates": tc.headers, "json_predicates": tc.predicates})
		if !result.IsError {
			t.Fatalf("expected error for %+v", tc)
		}
	}
	routine.RemoteID = "remote-machine"
	if err := srv.stateDB.UpdateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	result = callWebhookMCP(t, mcp, map[string]any{"action": "subscribe", "inbox_id": "inbox", "routine_id": routine.ID, "header_predicates": "{}", "json_predicates": "{}"})
	if !result.IsError {
		t.Fatal("remote routine accepted")
	}
	if subs, _ := srv.stateDB.ListWebhookSubscriptions(t.Context(), "inbox"); len(subs) != 0 {
		t.Fatalf("failed requests saved subscriptions: %+v", subs)
	}
}
