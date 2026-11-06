package sidebar

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func TestSidebarUnarchivedOutsideRecentWindow(t *testing.T) {
	r, core, _ := largeInventory(t)
	if _, err := core.SQL().Exec(`INSERT INTO archived_session(platform,session_id,session_time_updated,archived_at)
	 SELECT platform,id,updated_at,updated_at FROM session_catalog`); err != nil {
		t.Fatal(err)
	}
	const id = "ocm_00000001-0000-0000-0000-000000000001"
	query := platforms.SidebarQuery{Since: time.Now().UnixMilli()}
	scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	rows, err := r.Sessions(t.Context(), scope, query)
	if err != nil || len(rows) != 0 {
		t.Fatalf("archived history: rows=%d err=%v", len(rows), err)
	}
	if _, err := core.SQL().Exec(`DELETE FROM archived_session WHERE session_id=?`, id); err != nil {
		t.Fatal(err)
	}
	rows, err = r.Sessions(t.Context(), scope, query)
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Archived {
		t.Fatalf("unarchived history: rows=%+v err=%v", rows, err)
	}
	if err := core.ArchiveProject(t.Context(), "local", "/two"); err != nil {
		t.Fatal(err)
	}
	rows, err = r.Sessions(t.Context(), scope, query)
	if err != nil || len(rows) != 0 {
		t.Fatalf("archived project: rows=%+v err=%v", rows, err)
	}
	if _, err := core.SQL().Exec(`DELETE FROM archived_project`); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().Exec(`INSERT INTO archived_session(platform,session_id,session_time_updated,archived_at) VALUES('test',?,0,0)`, id); err != nil {
		t.Fatal(err)
	}
	rows, err = r.Sessions(t.Context(), scope, query)
	if err != nil || len(rows) != 1 || rows[0].Archived {
		t.Fatalf("resurfaced history: rows=%+v err=%v", rows, err)
	}
}
