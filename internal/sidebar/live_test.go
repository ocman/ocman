package sidebar

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestLiveReadsDoNotReadObservationTables(t *testing.T) {
	r, _, history := sidebarInventory(t, 10)
	r.snapshots = history.SidebarLiveJSON
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	instance := statev2.Instance{OwnerID: scope.Owner, Platform: scope.Platform, SourceKey: scope.Source}
	if err := history.BeginSidebarGeneration(t.Context(), instance, "g"); err != nil {
		t.Fatal(err)
	}
	if err := history.ReplaceSidebarObservations(t.Context(), instance, "g", time.Now().Add(time.Minute).UnixMilli(), map[string]statev2.SidebarObservation{"native-1": {Status: "busy"}}); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("DROP TABLE sidebar_observation; DROP TABLE sidebar_connection"); err != nil {
		t.Fatal(err)
	}
	count, err := r.RunningCount(t.Context(), []Scope{scope}, platforms.SidebarQuery{})
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v, want 1 without observation SQL", count, err)
	}
	rows, err := r.Sessions(t.Context(), scope, platforms.SidebarQuery{})
	if err != nil {
		t.Fatal(err)
	}
	busy := 0
	for _, row := range rows {
		if row.Status == "busy" {
			busy++
		}
	}
	if busy != 1 {
		t.Fatalf("busy=%d, want 1", busy)
	}
}

func TestDenseLiveReadsKeepIndexedSQLPlan(t *testing.T) {
	for _, raw := range []string{"", strings.Repeat("x", (64<<10)+1)} {
		r := Reader{snapshots: func(bool) string { return raw }}
		query, args := r.liveQuery(countRunning, []any{"scopes", "ids"})
		if query != countRunning || !reflect.DeepEqual(args, []any{"scopes", "ids"}) {
			t.Fatal("dense snapshot changed indexed SQL path")
		}
	}
}

func TestLiveReadsMatchDurableCheckpointsAndFences(t *testing.T) {
	r, core, history := sidebarInventory(t, 10)
	live := *r
	live.snapshots = history.SidebarLiveJSON
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	instance := statev2.Instance{OwnerID: scope.Owner, Platform: scope.Platform, SourceKey: scope.Source}
	if err := history.BeginSidebarGeneration(t.Context(), instance, "g"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().Exec(`UPDATE session_catalog SET parent_native_id='native-1' WHERE native_id='native-2'`); err != nil {
		t.Fatal(err)
	}
	observations := map[string]statev2.SidebarObservation{
		"native-1": {Status: "busy", BackgroundTasks: 2, BackgroundServices: 1},
		"native-2": {Status: "done", PendingPermission: true},
		"native-3": {Status: "busy"},
	}
	for _, expires := range []int64{time.Now().Add(time.Hour).UnixMilli(), 1, 0} {
		if err := history.ReplaceSidebarObservations(t.Context(), instance, "g", expires, observations); err != nil {
			t.Fatal(err)
		}
		for _, s := range []Scope{scope, {Owner: "other", Platform: "test"}, {Owner: "owner", Platform: "test", Source: "other", Generation: "g"}, {Owner: "owner", Platform: "test", Source: "source", Generation: "old"}} {
			want, err := r.Sessions(t.Context(), s, platforms.SidebarQuery{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := live.Sessions(t.Context(), s, platforms.SidebarQuery{})
			if err != nil || !reflect.DeepEqual(want, got) {
				t.Fatalf("scope=%v expiry=%d equivalent=%v err=%v", s, expires, reflect.DeepEqual(want, got), err)
			}
			n, err := r.RunningCount(t.Context(), []Scope{s}, platforms.SidebarQuery{})
			if err != nil {
				t.Fatal(err)
			}
			count, err := live.RunningCount(t.Context(), []Scope{s}, platforms.SidebarQuery{})
			if err != nil || count != n {
				t.Fatalf("count=%d want=%d err=%v", count, n, err)
			}
		}
	}
	row, err := live.Session(t.Context(), scope, "ocm_00000001-0000-0000-0000-000000000001")
	if err != nil || row.PendingPermission || row.BackgroundTaskCount != 2 {
		t.Fatalf("expired foreground / retained background: %+v %v", row, err)
	}
	if _, err := live.Session(t.Context(), scope, "missing"); err == nil {
		t.Fatal("missing row accepted")
	}
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`UPDATE session_sidebar SET source_deleted=1 WHERE id='ocm_00000001-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Session(t.Context(), scope, "ocm_00000001-0000-0000-0000-000000000001"); err == nil {
		t.Fatal("compact event surfaced deleted history")
	}
	if _, err := OpenLive("", "", nil); err == nil {
		t.Fatal("nil live source accepted")
	}
}
