package inbox

import (
	"context"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/testutil"
)

func TestInboxToolDiscoveryAndActions(t *testing.T) {
	store, err := state.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, err := mcptest.NewServer(t, inboxServerTools(&inboxTools{store: store})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	listed, err := srv.Client().ListTools(context.Background(), mcplib.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		found = found || tool.Name == "inbox"
	}
	if !found {
		t.Fatalf("inbox tool missing from %#v", listed.Tools)
	}

	help := testutil.CallTool(t, srv, "inbox", map[string]any{"action": "help"})
	for _, text := range []string{"send", "recall", "title", "body", "item_id", "output_schema"} {
		if !strings.Contains(testutil.ResultText(help), text) {
			t.Fatalf("help result missing %q: %s", text, testutil.ResultText(help))
		}
	}
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{}, want: "action is required"},
		{args: map[string]any{"action": "unknown"}, want: "unknown action"},
		{args: map[string]any{"action": "pin", "item_id": "id"}, want: "unknown action"},
		{args: map[string]any{"action": "unpin", "item_id": "id"}, want: "unknown action"},
		{args: map[string]any{"action": "recall", "item_id": " "}, want: "item_id is required"},
	} {
		result := testutil.CallTool(t, srv, "inbox", test.args)
		if !result.IsError || testutil.ResultText(result) != test.want {
			t.Fatalf("args %#v: result = %q", test.args, testutil.ResultText(result))
		}
	}
	for _, args := range []map[string]any{
		{"action": "send", "title": " ", "body": "body"},
		{"action": "send", "title": "title", "body": "\n"},
		{"action": "send", "title": "title", "body": "body", "session_id": "ses-1"},
		{"action": "send", "title": "title", "body": "body", "platform": "opencode"},
		{"action": "send", "title": "title", "body": "body", "platform": "r-other:opencode", "session_id": "ses-1"},
	} {
		if result := testutil.CallTool(t, srv, "inbox", args); !result.IsError {
			t.Fatalf("blank send input accepted: %#v", args)
		}
	}

	sent := testutil.CallTool(t, srv, "inbox", map[string]any{"action": "send", "title": "  Review  ", "body": "  Check this  "})
	items, err := store.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 || sent.IsError || !strings.Contains(testutil.ResultText(sent), items[0].ID) {
		t.Fatalf("send result = %q, items = %#v, err = %v", testutil.ResultText(sent), items, err)
	}
	if strings.Contains(testutil.ResultText(sent), "Review") || strings.Contains(testutil.ResultText(sent), "Check this") {
		t.Fatalf("send exposed more than the opaque ID: %s", testutil.ResultText(sent))
	}

	for _, id := range []string{items[0].ID, items[0].ID, "unknown"} {
		if result := testutil.CallTool(t, srv, "inbox", map[string]any{"action": "recall", "item_id": id}); result.IsError {
			t.Fatalf("recall %q failed: %s", id, testutil.ResultText(result))
		}
	}
	if items, err := store.ListInboxItems(t.Context()); err != nil || len(items) != 0 {
		t.Fatalf("items after recall = %#v, %v", items, err)
	}
	for _, category := range []string{"general", "factory", "routine"} {
		if result := testutil.CallTool(t, srv, "inbox", map[string]any{"action": "send", "title": category, "body": "body", "category": category, "platform": "opencode", "session_id": "ses-origin"}); result.IsError {
			t.Fatalf("category %s: %s", category, testutil.ResultText(result))
		}
	}
	items, err = store.ListInboxItems(t.Context())
	if err != nil || len(items) != 3 {
		t.Fatalf("categorized items: %+v, %v", items, err)
	}
	for _, item := range items {
		wantCategory := item.Title
		if wantCategory == "routine" {
			wantCategory = state.InboxGeneral
		}
		if item.Category != wantCategory || item.Session == nil || item.Session.Platform != "opencode" || item.Session.SessionID != "ses-origin" {
			t.Fatalf("category not stored: %+v", item)
		}
	}
	if result := testutil.CallTool(t, srv, "inbox", map[string]any{"action": "send", "title": "Fake permission", "body": "body", "category": "permission"}); !result.IsError {
		t.Fatal("accepted an actionable category from MCP")
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "send", "title": "title", "body": "body"},
		{"action": "recall", "item_id": "id"},
	} {
		result := testutil.CallTool(t, srv, "inbox", args)
		if !result.IsError || testutil.ResultText(result) != "inbox request failed" {
			t.Fatalf("closed store result = %q", testutil.ResultText(result))
		}
	}
}
