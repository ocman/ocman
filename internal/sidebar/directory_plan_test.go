package sidebar

import (
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestDirectoryPageUsesDirectoryRecency(t *testing.T) {
	r, _, history := sidebarInventory(t, 1000)
	for _, live := range []bool{false, true} {
		if live {
			r.snapshots = history.SidebarLiveJSON
		}
		args := sparseArgs(false)
		args[4], args[5] = "/one", "/one"
		query, args := r.liveQuery(sessionStatement(platforms.SidebarQuery{Directory: "/one", Limit: 50}), args)
		plan := queryPlan(t, r, query, args)
		found := false
		for _, detail := range plan {
			if strings.Contains(detail, "SEARCH s USING INDEX sidebar_directory_recency (directory=?)") {
				found = true
			}
			if strings.Contains(detail, "SCAN s USING INDEX sidebar_global_recency") {
				t.Fatalf("live=%v: directory page scans unrelated history: %v", live, plan)
			}
		}
		if !found {
			t.Fatalf("live=%v: directory page does not use directory recency: %v", live, plan)
		}
	}
}

func TestSessionStatementKeepsGlobalAndUnboundedPaths(t *testing.T) {
	for _, q := range []platforms.SidebarQuery{{}, {Directory: "/one"}} {
		if sessionStatement(q) != selectInventory {
			t.Fatal("unbounded inventory changed its driving order")
		}
	}
	if sessionStatement(platforms.SidebarQuery{Limit: 50}) != selectSessions {
		t.Fatal("global page changed its driving order")
	}
}
