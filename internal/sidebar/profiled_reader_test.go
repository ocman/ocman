package sidebar

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

// Investigation only: report the cost of the actual query for background
// inventories as well as browser pages, without changing polling or SQL.
func TestSidebarProfiledReadCosts(t *testing.T) {
	r, _, _ := largeInventory(t)
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	for _, tc := range []struct {
		name  string
		query platforms.SidebarQuery
		count int
	}{
		{"page", platforms.SidebarQuery{Limit: 50}, 50},
		{"directory inventory", platforms.SidebarQuery{Directory: "/one"}, 5000},
		{"full inventory", platforms.SidebarQuery{}, 10000},
	} {
		start := time.Now()
		rows, err := r.SessionsFor(t.Context(), []Scope{scope}, tc.query)
		if err != nil || len(rows) != tc.count {
			t.Fatalf("%s: %d rows, %v", tc.name, len(rows), err)
		}
		t.Logf("%s: %d rows from 10,000 sessions in %s", tc.name, len(rows), time.Since(start))
	}
}
