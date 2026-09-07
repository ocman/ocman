package mcp_test

import (
	"context"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
)

func TestInboxToolDiscoveryAndActions(t *testing.T) {
	store, err := state.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{InboxStore: store})...)
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

	help := callTool(t, srv, "inbox", map[string]any{"action": "help"})
	for _, text := range []string{"send", "recall", "title", "body", "item_id", "output_schema"} {
		if !strings.Contains(resultText(help), text) {
			t.Fatalf("help result missing %q: %s", text, resultText(help))
		}
	}
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{}, want: "action is required"},
		{args: map[string]any{"action": "unknown"}, want: "unknown action"},
		{args: map[string]any{"action": "recall", "item_id": " "}, want: "item_id is required"},
	} {
		result := callTool(t, srv, "inbox", test.args)
		if !result.IsError || resultText(result) != test.want {
			t.Fatalf("args %#v: result = %q", test.args, resultText(result))
		}
	}
	for _, args := range []map[string]any{
		{"action": "send", "title": " ", "body": "body"},
		{"action": "send", "title": "title", "body": "\n"},
	} {
		if result := callTool(t, srv, "inbox", args); !result.IsError {
			t.Fatalf("blank send input accepted: %#v", args)
		}
	}

	sent := callTool(t, srv, "inbox", map[string]any{"action": "send", "title": "  Review  ", "body": "  Check this  "})
	items, err := store.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 || sent.IsError || !strings.Contains(resultText(sent), items[0].ID) {
		t.Fatalf("send result = %q, items = %#v, err = %v", resultText(sent), items, err)
	}
	if strings.Contains(resultText(sent), "Review") || strings.Contains(resultText(sent), "Check this") {
		t.Fatalf("send exposed more than the opaque ID: %s", resultText(sent))
	}

	for _, id := range []string{items[0].ID, items[0].ID, "unknown"} {
		if result := callTool(t, srv, "inbox", map[string]any{"action": "recall", "item_id": id}); result.IsError {
			t.Fatalf("recall %q failed: %s", id, resultText(result))
		}
	}
	if items, err := store.ListInboxItems(t.Context()); err != nil || len(items) != 0 {
		t.Fatalf("items after recall = %#v, %v", items, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "send", "title": "title", "body": "body"},
		{"action": "recall", "item_id": "id"},
	} {
		result := callTool(t, srv, "inbox", args)
		if !result.IsError || resultText(result) != "inbox request failed" {
			t.Fatalf("closed store result = %q", resultText(result))
		}
	}
}
