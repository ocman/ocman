package statev2

import (
	"path/filepath"
	"testing"
)

func removeIncrementalAnalyticsSchema(t *testing.T, d *DB) {
	t.Helper()
	removePreparationSchema(t, d)
	if _, err := d.db.Exec(`ALTER TABLE analytics_session DROP COLUMN is_subagent; DROP INDEX analytics_fact_hour`); err != nil {
		t.Fatal(err)
	}
}

func removePreparationSchema(t *testing.T, d *DB) {
	t.Helper()
	removeBackgroundSchema(t, d)
	if _, err := d.db.Exec(`DROP TRIGGER IF EXISTS session_preparation_legacy_insert;
		DROP TABLE IF EXISTS session_identity_plan; DROP TABLE IF EXISTS session_identity_inventory;
		DROP TABLE IF EXISTS session_preparation`); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalAnalyticsUpgradesShippedV13(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	b.Session.Subagent = true
	commit(t, d, b)
	removeIncrementalAnalyticsSchema(t, d)
	if _, err := d.db.Exec(`PRAGMA user_version=13`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var subagent bool
	if err := d.db.QueryRow(`SELECT is_subagent FROM analytics_session WHERE instance_id='first' AND id='session'`).Scan(&subagent); err != nil || !subagent {
		t.Fatalf("v13 history upgrade lost subagent evidence: %v %v", subagent, err)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatalf("v13 checkpoint changed: %q %v", cursor, err)
	}
}
