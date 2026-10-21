package statev2

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRekeySessionsPreservesHistoryReceiptsAndOtherSources(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	b := fixture("first")
	b.Session.ParentID = "missing-parent"
	b.Session.Extensions = json.RawMessage(`{"ParentID":"missing-parent","opaque":{"sessionID":"session"}}`)
	b.Parts[0].Extensions = json.RawMessage(`{"input":{"sessionID":"session"}}`)
	b.Checkpoint.Stream = "history/snapshot-v1/session"
	commit(t, d, b)
	other := fixture("second")
	commit(t, d, other)
	if err := d.SetSourceState(t.Context(), "first", "read-ready/session", "version"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSourceState(t.Context(), "first", "version/session", "version"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO checkpoint VALUES('first','seq/session','7'); INSERT INTO purged_session VALUES('first','purged')`); err != nil {
		t.Fatal(err)
	}
	before := read(t, d, b.Session.Key)
	beforeChanges := changes(t, d)
	ids := map[string]string{"session": "ocm_session", "missing-parent": "ocm_parent", "purged": "ocm_purged"}
	if err := d.RekeySessions(t.Context(), "first", ids); err != nil {
		t.Fatal(err)
	}
	after := read(t, d, Key{"first", "ocm_session"})
	if after.Session.Revision != before.Session.Revision || after.Session.Title != before.Session.Title || after.Session.ParentID != "ocm_parent" || after.Messages[0].ID != "message" || after.Parts[0].ID != "part" || after.Parts[0].MessageID != "message" || after.Parts[0].Text != before.Parts[0].Text || string(after.Parts[0].Extensions) != string(before.Parts[0].Extensions) {
		t.Fatalf("history changed beyond session references: %+v", after)
	}
	var ext map[string]any
	if err := json.Unmarshal(after.Session.Extensions, &ext); err != nil {
		t.Fatal(err)
	}
	if ext["ParentID"] != "ocm_parent" || ext["opaque"].(map[string]any)["sessionID"] != "session" {
		t.Fatalf("extensions: %s", after.Session.Extensions)
	}
	if _, err := d.Read(t.Context(), b.Session.Key, 10, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy session still exists: %v", err)
	}
	if got := read(t, d, other.Session.Key); got.Session.SessionID != "session" {
		t.Fatal("another source was rewritten")
	}
	state, err := d.SourceState(t.Context(), "first")
	if err != nil || state["read-ready/ocm_session"] != "version" || state["version/ocm_session"] != "version" || len(state) != 2 {
		t.Fatalf("receipts: %+v %v", state, err)
	}
	for stream, want := range map[string]string{"history/snapshot-v1/ocm_session": "1", "seq/ocm_session": "7"} {
		if got, err := d.Checkpoint(t.Context(), "first", stream); err != nil || got != want {
			t.Fatalf("checkpoint %s: %s %v", stream, got, err)
		}
	}
	_, purged, err := d.SessionState(t.Context(), Key{"first", "ocm_purged"})
	if err != nil || !purged {
		t.Fatalf("purge tombstone lost: %v %v", purged, err)
	}
	afterChanges := changes(t, d)
	if len(afterChanges) != len(beforeChanges) {
		t.Fatal("rekey emitted or discarded historical changes")
	}
	for i := range afterChanges {
		if afterChanges[i].Sequence != beforeChanges[i].Sequence || afterChanges[i].Revision != beforeChanges[i].Revision {
			t.Fatal("outbox cursor changed")
		}
		if afterChanges[i].InstanceID == "first" && afterChanges[i].SessionID != "ocm_session" {
			t.Fatal("outbox still contains legacy session key")
		}
	}
	for _, ref := range sessionKeyColumns {
		var count int
		if err := d.db.QueryRow("SELECT count(*) FROM "+ref.table+" WHERE instance_id=? AND "+ref.column+"=?", "first", ref.prefix+"session").Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy reference retained in %s.%s: %d %v", ref.table, ref.column, count, err)
		}
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(d.path), ".statev2-before-session-ids-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup missing: %+v %v", backups, err)
	}
	info, err := os.Stat(backups[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup permissions: %+v %v", info, err)
	}
	backup, err := Open(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if got := read(t, backup, b.Session.Key); got.Session.ParentID != "missing-parent" {
		t.Fatal("backup did not preserve original keys")
	}
	if err := d.RekeySessions(t.Context(), "first", ids); err != nil {
		t.Fatal(err)
	}
	if replay := read(t, d, Key{"first", "ocm_session"}); !reflect.DeepEqual(after, replay) {
		t.Fatal("repeated rekey changed history")
	}
	backups, _ = filepath.Glob(filepath.Join(filepath.Dir(d.path), ".statev2-before-session-ids-*.db"))
	if len(backups) != 1 {
		t.Fatal("repeated rekey created another backup")
	}
}

func TestSessionRekeyContractCoversNormalizedColumns(t *testing.T) {
	d := openTestDB(t)
	rows, err := d.db.Query(`SELECT m.name,p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type='table' AND (p.name='session_id' OR p.name='parent_id' OR (p.name='id' AND m.name IN ('session','analytics_session','session_sidebar')))`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	covered := map[string]bool{}
	for _, ref := range sessionKeyColumns {
		if ref.prefix == "" {
			covered[ref.table+"."+ref.column] = true
		}
	}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		key := table + "." + column
		if !covered[key] {
			t.Fatalf("normalized reference missing from session rekey: %s", key)
		}
		delete(covered, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(covered) != 0 {
		t.Fatalf("session rekey refers to missing schema columns: %+v", covered)
	}
	for _, ref := range sessionKeyColumns {
		if ref.prefix != "" && !strings.HasSuffix(ref.prefix, "/") {
			t.Fatalf("packed session key has no exact prefix boundary: %+v", ref)
		}
	}
}

func TestRekeySessionsRollsBackConflicts(t *testing.T) {
	for _, conflict := range []string{"retained session", "purge tombstone", "checkpoint"} {
		t.Run(conflict, func(t *testing.T) {
			d := openTestDB(t)
			b := fixture("first")
			commit(t, d, b)
			switch conflict {
			case "retained session":
				other := fixture("first")
				other.Session.SessionID = "new"
				other.Checkpoint = nil
				commit(t, d, other)
			case "purge tombstone":
				if _, err := d.db.Exec(`INSERT INTO purged_session VALUES('first','new')`); err != nil {
					t.Fatal(err)
				}
			case "checkpoint":
				if _, err := d.db.Exec(`INSERT INTO checkpoint VALUES('first','seq/session','1'),('first','seq/new','2')`); err != nil {
					t.Fatal(err)
				}
			}
			before := read(t, d, b.Session.Key)
			if err := d.RekeySessions(t.Context(), "first", map[string]string{"session": "new"}); err == nil {
				t.Fatal("conflicting keys were combined")
			}
			if got := read(t, d, b.Session.Key); !reflect.DeepEqual(got, before) {
				t.Fatal("failed migration partially changed history")
			}
		})
	}
}
