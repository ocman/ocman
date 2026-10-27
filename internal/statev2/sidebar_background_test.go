package statev2

import "testing"

func removeBackgroundSchema(t *testing.T, d *DB) {
	t.Helper()
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM pragma_table_info('sidebar_observation') WHERE name='background_tasks'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		return
	}
	if _, err := d.db.Exec(`ALTER TABLE sidebar_observation DROP COLUMN background_tasks; ALTER TABLE sidebar_observation DROP COLUMN background_services`); err != nil {
		t.Fatal(err)
	}
}
