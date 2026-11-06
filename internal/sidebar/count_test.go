package sidebar

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestRunningCountMatchesLiveSidebarVisibility(t *testing.T) {
	r, core, history := largeInventory(t)
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	instance := statev2.Instance{OwnerID: scope.Owner, Platform: scope.Platform, SourceKey: scope.Source}
	if err := history.BeginSidebarGeneration(t.Context(), instance, "g"); err != nil {
		t.Fatal(err)
	}
	observations := map[string]statev2.SidebarObservation{
		"native-1":  {Status: "busy"},
		"native-2":  {Status: "busy", PendingPermission: true},
		"native-3":  {Status: "waiting"},
		"native-4":  {Status: "busy"},
		"native-5":  {Status: "done", PendingQuestion: true},
		"native-6":  {Status: "busy"},
		"native-7":  {Status: "busy"},
		"native-8":  {Status: "busy"},
		"native-9":  {Status: "busy"},
		"native-10": {Status: "busy"},
	}
	if err := history.ReplaceSidebarObservations(t.Context(), instance, "g", time.Now().Add(time.Hour).UnixMilli(), observations); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().Exec(`UPDATE session_catalog SET parent_native_id='native-4' WHERE native_id='native-5';
	 UPDATE session_catalog SET source_key='retained' WHERE native_id='native-9'`); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`UPDATE session_sidebar SET is_subagent=1 WHERE updated_at IN (6,7);
	 UPDATE session_sidebar SET source_child=1 WHERE updated_at=7;
	 UPDATE session_sidebar SET source_deleted=1 WHERE updated_at=8;
	 UPDATE session_sidebar SET inferred_status='busy' WHERE updated_at=11`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		q    platforms.SidebarQuery
		want int
	}{
		{"all", platforms.SidebarQuery{}, 3},
		{"busy ignores time window and paging", platforms.SidebarQuery{Since: 999999, Limit: 1, Offset: 999}, 3},
		{"directory", platforms.SidebarQuery{Directory: "/one"}, 1},
		{"origin", platforms.SidebarQuery{Origin: "routine", OriginID: "r"}, 1},
		{"wrong origin", platforms.SidebarQuery{OriginID: "unknown"}, 0},
		{"included deleted", platforms.SidebarQuery{IncludeIDs: []string{"ocm_00000008-0000-0000-0000-000000000008"}}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count, err := r.RunningCount(t.Context(), []Scope{scope}, tc.q)
			if err != nil || count != tc.want {
				t.Fatalf("count=%d want=%d err=%v", count, tc.want, err)
			}
			q := tc.q
			q.Limit, q.Offset = 0, 0
			rows, err := r.Sessions(t.Context(), scope, q)
			if err != nil {
				t.Fatal(err)
			}
			busy := 0
			for _, row := range rows {
				if row.Status == db.StatusBusy {
					busy++
				}
			}
			if count != busy {
				t.Fatalf("aggregate=%d sidebar busy=%d", count, busy)
			}
		})
	}
	for _, scopes := range [][]Scope{nil, {{Owner: "other", Platform: "test"}}, {{Owner: "owner", Platform: "test", Source: "source", Generation: "old"}}, {{Owner: "owner", Platform: "test", Source: "other", Generation: "g"}}} {
		if n, err := r.RunningCount(t.Context(), scopes, platforms.SidebarQuery{}); err != nil || n != 0 {
			t.Fatalf("scope fence: count=%d err=%v", n, err)
		}
	}
	for _, scopes := range [][]Scope{{{Platform: "test"}}, {scope, {Owner: "other", Platform: "test"}}} {
		if _, err := r.RunningCount(t.Context(), scopes, platforms.SidebarQuery{}); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	if err := history.ReplaceSidebarObservations(t.Context(), instance, "g", 1, observations); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RunningCount(t.Context(), []Scope{scope}, platforms.SidebarQuery{}); err != nil || n != 0 {
		t.Fatalf("expired source counted: %d %v", n, err)
	}
	_ = r.Close()
	if _, err := r.RunningCount(t.Context(), []Scope{scope}, platforms.SidebarQuery{}); err == nil {
		t.Fatal("closed store accepted")
	}
}
