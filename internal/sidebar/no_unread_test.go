package sidebar

import (
	"database/sql"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestSidebarDoesNotReadMessageCounts(t *testing.T) {
	r, _, history := largeInventory(t)
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The compact browsing contract must not depend on the message table.
	if _, err := conn.Exec(`ALTER TABLE message RENAME TO unavailable_messages`); err != nil {
		t.Fatal(err)
	}
	rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, platforms.SidebarQuery{Limit: 50})
	if err != nil || len(rows) != 50 {
		t.Fatalf("sidebar depends on messages: rows=%d err=%v", len(rows), err)
	}
	if n, err := r.RunningCount(t.Context(), []Scope{{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}}, platforms.SidebarQuery{}); err != nil || n != 0 {
		t.Fatalf("aggregate depends on messages: count=%d err=%v", n, err)
	}
}
