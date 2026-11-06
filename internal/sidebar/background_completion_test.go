package sidebar

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestSidebarRecordsFinalBackgroundTaskCompletion(t *testing.T) {
	for _, omitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit zero", true: "snapshot omission"}[omitted], func(t *testing.T) {
			r, _, history := largeInventory(t)
			scope := statev2.Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
			if err := history.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
				t.Fatal(err)
			}
			write := func(rows map[string]statev2.SidebarObservation) {
				t.Helper()
				if err := history.ReplaceSidebarObservations(t.Context(), scope, "g", time.Now().Add(time.Hour).UnixMilli(), rows); err != nil {
					t.Fatal(err)
				}
			}
			write(map[string]statev2.SidebarObservation{"native-1": {Status: "idle", BackgroundTasks: 1}})
			before := time.Now().UnixMilli()
			next := map[string]statev2.SidebarObservation{"native-1": {Status: "idle"}}
			if omitted {
				next = nil
			}
			write(next)
			read := func() int64 {
				t.Helper()
				rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, platforms.SidebarQuery{})
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					if row.ID == "ocm_00000001-0000-0000-0000-000000000001" {
						return row.LastTurnCompletedAt
					}
				}
				t.Fatal("session missing")
				return 0
			}
			completed := read()
			if completed < before || completed > time.Now().UnixMilli() {
				t.Fatalf("background completion=%d, want timestamp >=%d", completed, before)
			}
			write(next)
			if got := read(); got != completed {
				t.Fatalf("unchanged snapshot moved completion: %d -> %d", completed, got)
			}
		})
	}
}
