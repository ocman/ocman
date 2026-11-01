package sidebar

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestSidebarNativeSubagentVisibility(t *testing.T) {
	for _, tc := range []struct {
		name, status                  string
		child, prompt, included, want bool
	}{
		{name: "running child", status: "busy", child: true, want: true},
		{name: "completed child", status: "done", child: true},
		{name: "child prompt belongs to parent", status: "busy", child: true, prompt: true},
		{name: "opened completed child", status: "done", child: true, included: true, want: true},
		{name: "root helper stays hidden", status: "busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, core, history := largeInventory(t)
			const id = "ocm_00000001-0000-0000-0000-000000000001"
			const parent = "ocm_00000002-0000-0000-0000-000000000002"
			conn, err := sql.Open("sqlite", history.FilePath())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			parentID := ""
			if tc.child {
				parentID = parent
			}
			if _, err := conn.Exec(`UPDATE session_sidebar SET is_subagent=1,source_child=?,parent_id=? WHERE id=?`, tc.child, parentID, id); err != nil {
				t.Fatal(err)
			}
			if tc.child {
				if _, err := core.SQL().Exec(`UPDATE session_catalog SET parent_id=?,parent_native_id='native-2' WHERE id=?`, parentID, id); err != nil {
					t.Fatal(err)
				}
			}
			scope := statev2.Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
			if err := history.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
				t.Fatal(err)
			}
			if err := history.ReplaceSidebarObservations(t.Context(), scope, "g", time.Now().Add(time.Minute).UnixMilli(), map[string]statev2.SidebarObservation{
				"native-1": {Status: tc.status, PendingPermission: tc.prompt},
			}); err != nil {
				t.Fatal(err)
			}
			query := platforms.SidebarQuery{}
			if tc.included {
				query.IncludeIDs = []string{id}
			}
			rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, query)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.ID == id {
					found = true
					if row.ParentID != parentID {
						t.Fatalf("parent = %q, want %q", row.ParentID, parentID)
					}
					if tc.status == "busy" && row.Status != db.StatusBusy {
						t.Fatalf("status = %q, want busy", row.Status)
					}
				}
			}
			if found != tc.want {
				t.Fatalf("subagent visible = %v, want %v", found, tc.want)
			}
		})
	}
}
