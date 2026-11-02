package statev2

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestUpgradeDropsPurgedSessionAndKeepsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := migrationIndex(t, retirePurgedSessionSchema) + 1; v != 20 {
		t.Fatalf("purged_session drop is v%d; the README names v20", v)
	}
	register(t, d, "first")
	commit(t, d, fixture("first"))
	if _, err := d.db.Exec(`CREATE TABLE purged_session (
		instance_id TEXT NOT NULL REFERENCES instance(id),
		session_id TEXT NOT NULL CHECK(session_id <> ''),
		PRIMARY KEY(instance_id, session_id)
	); PRAGMA user_version=` + fmt.Sprint(migrationIndex(t, retirePurgedSessionSchema))); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var tables, sessions int
	if err := d.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='purged_session'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("purged_session after upgrade = %d, %v", tables, err)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM session`).Scan(&sessions); err != nil || sessions == 0 {
		t.Fatalf("sessions after upgrade = %d, %v", sessions, err)
	}
}

// migrationIndex returns the schema version just before schema applies.
func migrationIndex(t *testing.T, schema string) int {
	t.Helper()
	for i, migration := range migrations {
		if migration == schema {
			return i
		}
	}
	t.Fatal("migration not registered")
	return 0
}
