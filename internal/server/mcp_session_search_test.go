package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/ocman/ocman/internal/db"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/testutil"
)

func TestMCPSearchFindsSessionsOutsideRecent500(t *testing.T) {
	srv := testServer(t)
	now := time.Now().UnixMilli()
	sessions := make([]db.Session, 501)
	for i := range sessions {
		sessions[i] = db.Session{ID: fmt.Sprintf("recent-%03d", i), Title: "Unrelated", Directory: "/repo", TimeUpdated: now}
	}
	sessions = append(sessions, db.Session{ID: "old-bundles", Title: "Protecting JS bundles from unauthenticated users", Directory: "/repo", TimeUpdated: now - 30*24*60*60*1000})
	srv.registry.Register(&fakePlatform{PlatformID: "test", SessionList: sessions})
	if err := srv.stateDB.ArchiveSession(t.Context(), "test", "old-bundles", sessions[501].TimeUpdated); err != nil {
		t.Fatal(err)
	}
	history := storedProjectFixture(t, srv)
	_, err := history.Commit(t.Context(), statev2.Batch{
		Session:  statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: "old-bundles"}, Title: sessions[501].Title, Directory: "/repo", CreatedAt: 1, UpdatedAt: sessions[501].TimeUpdated},
		Messages: []statev2.Message{{ID: "m1", Role: "user"}},
		Parts:    []statev2.Part{{ID: "p1", MessageID: "m1", Kind: "text", Text: "restrict anonymous access"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: sessionMCPService{srv}})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mcp.Close)
	for _, content := range []bool{false, true} {
		t.Run(fmt.Sprintf("content=%t", content), func(t *testing.T) {
			query := "bundles"
			if content {
				query = "restrict anonymous"
			}
			result := testutil.CallTool(t, mcp, "sessions", map[string]any{"action": "search", "query": query, "content": content, "since_days": 365, "directory": "/repo", "limit": 1})
			text := testutil.ResultText(result)
			if result.IsError || !strings.Contains(text, "old-bundles") || strings.Contains(text, "recent-") {
				t.Fatalf("older session missing from search: %s", text)
			}
			if content && !strings.Contains(text, `"partId": "p1"`) {
				t.Fatalf("content match missing: %s", text)
			}
			if !strings.Contains(text, `"archived": true`) {
				t.Fatalf("archived search result lost its state: %s", text)
			}
		})
	}
	listed := testutil.CallTool(t, mcp, "sessions", map[string]any{"action": "list", "limit": 1})
	if listed.IsError || strings.Contains(testutil.ResultText(listed), "old-bundles") {
		t.Fatalf("listing did not retain its recent-result limit: %s", testutil.ResultText(listed))
	}
	limited := testutil.CallTool(t, mcp, "sessions", map[string]any{"action": "search", "query": "Unrelated", "limit": 1})
	if limited.IsError || strings.Count(testutil.ResultText(limited), `"id": "recent-`) != 1 {
		t.Fatalf("search result limit not applied: %s", testutil.ResultText(limited))
	}
}
