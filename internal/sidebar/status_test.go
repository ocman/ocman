package sidebar

import (
	"database/sql"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestSidebarToolCallTurnMatchesStoredSummaryStatus(t *testing.T) {
	for _, finish := range []string{"tool-calls", "unknown"} {
		r, _, history := largeInventory(t)
		conn, err := sql.Open("sqlite", history.FilePath())
		if err != nil {
			t.Fatal(err)
		}
		const id = "ocm_00000001-0000-0000-0000-000000000001"
		if _, err := conn.Exec(`UPDATE session_sidebar SET last_finish=?,inferred_status='' WHERE id=?`, finish, id); err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
		rows, err := r.Sessions(t.Context(), Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}, platforms.SidebarQuery{Directory: "/two"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.ID == id {
				found = true
				if row.Status != db.StatusInterrupted {
					t.Fatalf("%s: sidebar status %s, want interrupted like the stored summary", finish, row.Status)
				}
			}
		}
		if !found {
			t.Fatal("fixture session missing")
		}
	}
}
