package statev2

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestAnalyticsSummaryUpgradePreservesHistoryAndCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	commit(t, d, b)
	if err := d.SetSourceState(t.Context(), "first", "version/session", "v1"); err != nil {
		t.Fatal(err)
	}
	purged := b
	purged.Session.SessionID = "purged"
	purged.Checkpoint = nil
	commit(t, d, purged)
	if err := d.Purge(t.Context(), purged.Session.Key); err != nil {
		t.Fatal(err)
	}
	// Recreate the immediately preceding schema without altering its payloads.
	for _, table := range []string{"analytics_question", "analytics_hour", "analytics_fact", "analytics_session", "analytics_conflict"} {
		if _, err := d.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	removeConversationColumns(t, d)
	if _, err := d.db.Exec(`PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got := read(t, d, b.Session.Key); got.Session.Revision != 1 || got.Parts[0].Text != "retained content" {
		t.Fatal("upgrade discarded history")
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatal("upgrade changed checkpoint", cursor, err)
	}
	if hints, err := d.SourceState(t.Context(), "first"); err != nil || hints["version/session"] != "v1" {
		t.Fatal("upgrade lost source hints", hints, err)
	}
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM analytics_fact`).Scan(&count); err != nil || count != 1 {
		t.Fatal("summary not backfilled", count, err)
	}
	if result := commit(t, d, purged); !result.Suppressed {
		t.Fatal("upgrade lost purge suppression")
	}
}

func TestAnalyticsSummaryUpgradeFailureRollsBackSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	commit(t, d, b)
	for _, table := range []string{"analytics_question", "analytics_hour", "analytics_fact", "analytics_session", "analytics_conflict"} {
		if _, err := d.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	removeConversationColumns(t, d)
	if _, err := d.db.Exec(`INSERT INTO analytics_part VALUES('first','session','message','malformed','{"state":1}'); PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if upgraded, err := Open(path); err == nil {
		upgraded.Close()
		t.Fatal("expected backfill failure")
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var version, count int
	if err := conn.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 5 {
		t.Fatal("advanced failed migration", version, err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM session`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed upgrade lost history", count, err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='analytics_fact'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partially committed summary schema", count, err)
	}
}

func TestAnalyticsIdentityUpgradeBackfillsConflictsWithoutRebuildingSummaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		register(t, d, id)
		commit(t, d, fixture(id))
	}
	for _, trigger := range []string{"analytics_session_insert", "analytics_session_delete", "analytics_fact_insert", "analytics_fact_delete"} {
		if _, err := d.db.Exec(`DROP TRIGGER ` + trigger); err != nil {
			t.Fatal(err)
		}
	}
	removeConversationColumns(t, d)
	if _, err := d.db.Exec(`DROP TABLE analytics_conflict; PRAGMA user_version=6`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM analytics_conflict`).Scan(&count); err != nil || count != 2 {
		t.Fatal("conflicts were not backfilled", count, err)
	}
	if got := read(t, d, fixture("first").Session.Key); got.Session.Revision != 1 {
		t.Fatal("identity upgrade rewrote history")
	}
	if err := d.Purge(t.Context(), fixture("second").Session.Key); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM analytics_conflict`).Scan(&count); err != nil || count != 0 {
		t.Fatal("purge retained obsolete conflict markers", count, err)
	}
}

func removeConversationColumns(t *testing.T, d *DB) {
	t.Helper()
	var summaries bool
	if err := d.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='analytics_session')`).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries {
		if _, err := d.db.Exec(`ALTER TABLE analytics_session DROP COLUMN is_subagent; DROP INDEX analytics_fact_hour`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.db.Exec(`DROP INDEX message_conversation_page;
		ALTER TABLE session DROP COLUMN is_subagent;
		ALTER TABLE session DROP COLUMN read_format;
		ALTER TABLE message DROP COLUMN conversation_visible;
		ALTER TABLE part DROP COLUMN created_at;
		ALTER TABLE part DROP COLUMN text_present;`); err != nil {
		t.Fatal(err)
	}
}

// Rows imported before adapters marked subagents keep the title rule
// analytics filtered on, so upgrading does not change their counts.
func TestSubagentUpgradeBackfillsLegacyTitleRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	for id, title := range map[string]string{"root": "Root", "child": "Explore (@explore subagent)"} {
		b := fixture("first")
		b.Session.SessionID, b.Session.Title, b.Checkpoint = id, title, nil
		commit(t, d, b)
	}
	if _, err := d.db.Exec(fmt.Sprintf(`ALTER TABLE session DROP COLUMN is_subagent; ALTER TABLE analytics_session DROP COLUMN is_subagent; DROP INDEX analytics_fact_hour; PRAGMA user_version=%d`, len(migrations)-2)); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for id, want := range map[string]bool{"root": false, "child": true} {
		if got := read(t, d, Key{InstanceID: "first", SessionID: id}).Session.Subagent; got != want {
			t.Errorf("%s subagent = %v, want %v", id, got, want)
		}
	}
}
