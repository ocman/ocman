package sidebar

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestSidebarRunningDrawersOutsideRecentWindow(t *testing.T) {
	r, core, history := largeInventory(t)
	const id = "ocm_00000001-0000-0000-0000-000000000001"
	scope := statev2.Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if err := history.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
		t.Fatal(err)
	}
	query := platforms.SidebarQuery{Since: time.Now().UnixMilli()}
	for _, origin := range []string{"external", "routine", "plugin"} {
		t.Run(origin, func(t *testing.T) {
			originID := "run"
			if origin == "external" {
				originID = ""
			}
			if _, err := core.SQL().Exec(`UPDATE session_catalog SET origin=?,origin_id=? WHERE id=?`, origin, originID, id); err != nil {
				t.Fatal(err)
			}
			for _, status := range []string{"busy", "done"} {
				if err := history.ReplaceSidebarObservations(t.Context(), scope, "g", time.Now().Add(time.Minute).UnixMilli(), map[string]statev2.SidebarObservation{
					"native-1": {Status: status},
				}); err != nil {
					t.Fatal(err)
				}
				rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, query)
				if err != nil {
					t.Fatal(err)
				}
				// All live foreground runs remain available, even without a recent message.
				want := 0
				if status == "busy" {
					want = 1
				}
				if len(rows) != want {
					t.Fatalf("%s: got %d rows, want %d", status, len(rows), want)
				}
			}
		})
	}
}
