package statev2

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSidebarDirectoryUpgradePreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	commit(t, d, fixture("first"))
	// Reopen at the last shipped schema, retaining existing projected history.
	previous := migrationIndex(t, sidebarGlobalRecencySchema) + 1
	if _, err := d.db.Exec(`DROP INDEX IF EXISTS sidebar_directory_recency; PRAGMA user_version=` + fmt.Sprint(previous)); err != nil {
		d.Close()
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='sidebar_directory_recency'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("directory index after upgrade=%d err=%v", count, err)
	}
	rows, err := d.db.Query(`SELECT name,desc FROM pragma_index_xinfo('sidebar_directory_recency') WHERE key=1 ORDER BY seqno`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		var descending int
		if err := rows.Scan(&name, &descending); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, fmt.Sprintf("%s:%d", name, descending))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"directory:0", "updated_at:1", "id:0"}) {
		t.Fatalf("directory index columns=%v", columns)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM session_sidebar`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sidebar history after upgrade=%d err=%v", count, err)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM message`).Scan(&count); err != nil || count == 0 {
		t.Fatalf("conversation history after upgrade=%d err=%v", count, err)
	}
}
