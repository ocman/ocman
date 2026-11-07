package statev2

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestSidebarRecencyUpgradePreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	commit(t, d, fixture("first"))
	if _, err := d.db.Exec(`DROP INDEX sidebar_global_recency; PRAGMA user_version=` + fmt.Sprint(migrationIndex(t, sidebarGlobalRecencySchema))); err != nil {
		d.Close()
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var indexes, sessions int
	if err := d.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='sidebar_global_recency'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Fatalf("recency index after upgrade=%d err=%v", indexes, err)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM session_sidebar`).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sidebar history after upgrade=%d err=%v", sessions, err)
	}
}
