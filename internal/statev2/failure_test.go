package statev2

import "testing"

// A corrupt/incomplete database must return errors, not acknowledge ingestion
// or make missing history look like a successful empty result.
func TestMissingTablesFailClosed(t *testing.T) {
	for _, table := range []string{"checkpoint", "purged_session", "session"} {
		t.Run("commit/"+table, func(t *testing.T) {
			d := openTestDB(t)
			if _, err := d.db.Exec("DROP TABLE " + table); err != nil {
				t.Fatal(err)
			}
			b := fixture("first")
			if table != "checkpoint" {
				b.Checkpoint = nil
			}
			if _, err := d.Commit(t.Context(), b); err == nil {
				t.Fatal("acknowledged incomplete storage")
			}
		})
	}
	for _, table := range []string{"message", "part"} {
		t.Run("read/"+table, func(t *testing.T) {
			d := openTestDB(t)
			b := fixture("first")
			commit(t, d, b)
			if _, err := d.db.Exec("DROP TABLE part"); err != nil {
				t.Fatal(err)
			}
			if table == "message" {
				if _, err := d.db.Exec("DROP TABLE message"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.Read(t.Context(), b.Session.Key, 1, 0); err == nil {
				t.Fatal("returned successful incomplete history")
			}
		})
	}
}

func TestInstanceNamePersistenceFailure(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.db.Exec(`CREATE TRIGGER fail_name BEFORE UPDATE ON instance
		BEGIN SELECT RAISE(ABORT, 'test update failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.RegisterInstance(t.Context(), Instance{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "first", Name: "lost"}); err == nil {
		t.Fatal("accepted failed name update")
	}
	if got, err := d.Instance(t.Context(), "first"); err != nil || got.Name != "" {
		t.Fatalf("partial name update: %+v %v", got, err)
	}
}

func TestPurgeNotificationFailureRetainsHistory(t *testing.T) {
	for _, setup := range []string{
		"DROP TABLE change_outbox",
		`CREATE TRIGGER fail_notification BEFORE INSERT ON change_outbox
		 BEGIN SELECT RAISE(ABORT, 'test outbox failure'); END`,
	} {
		t.Run(setup, func(t *testing.T) {
			d := openTestDB(t)
			b := fixture("first")
			commit(t, d, b)
			if _, err := d.db.Exec(setup); err != nil {
				t.Fatal(err)
			}
			if err := d.Purge(t.Context(), b.Session.Key); err == nil {
				t.Fatal("accepted purge without durable notification")
			}
			if got := read(t, d, b.Session.Key); len(got.Parts) != 1 {
				t.Fatal("failed purge removed history")
			}
		})
	}
}
