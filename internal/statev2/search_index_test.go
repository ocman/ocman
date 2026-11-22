package statev2

import (
	"fmt"
	"testing"
)

func TestSearchIndexBroadMatchesKeepNewestFirst(t *testing.T) {
	d := openTestDB(t)
	for i := range 36 {
		b := Batch{Session: Session{Key: Key{"first", fmt.Sprint(i)}, UpdatedAt: int64(i)},
			Messages: []Message{{ID: "m", Role: "assistant", ConversationVisible: true}}}
		for j := range 15 {
			text := "common phrase in every session"
			if i < 20 {
				text += " with older-only results"
			}
			b.Parts = append(b.Parts, Part{ID: fmt.Sprint(j), MessageID: "m", Kind: "text", Text: text})
		}
		commit(t, d, b)
	}
	for query, latest := range map[string]int{"common phrase": 35, "older-only": 19} {
		got, err := d.SearchSessionText(t.Context(), "owner", query)
		if err != nil || len(got) != 20 {
			t.Fatalf("%q: got %d results, %v", query, len(got), err)
		}
		for i, hit := range got {
			if hit.ID != fmt.Sprint(latest-i) {
				t.Fatalf("%q: result %d is %q; want %d", query, i, hit.ID, latest-i)
			}
		}
	}
}

func TestSearchIndexTracksChanges(t *testing.T) {
	d := openTestDB(t)
	key := Key{"first", "search"}
	b := Batch{Session: Session{Key: key}, Messages: []Message{{ID: "m", Role: "assistant", ConversationVisible: true}},
		Parts: []Part{{ID: "p", MessageID: "m", Kind: "text", Text: `A café said "look" OR nothing.`}}}
	check := func(query string, want int) {
		t.Helper()
		got, err := d.SearchSessionText(t.Context(), "owner", query)
		if err != nil || len(got) != want {
			t.Fatalf("%q: got %+v, %v; want %d", query, got, err, want)
		}
	}
	commit(t, d, b)
	check(`"LOOK" OR`, 1)
	check("afé", 1)
	check("OR", 0)
	check("café\x00said", 0)
	check(`" OR bogus`, 0)
	b.ExpectedRevision++
	b.Parts[0].Text = "An updated answer"
	commit(t, d, b)
	check("look", 0)
	check("updated", 1)
	b.ExpectedRevision++
	b.Messages[0].ConversationVisible = false
	b.Parts = nil
	commit(t, d, b)
	check("updated", 0)
	b.ExpectedRevision++
	b.Messages[0].ConversationVisible = true
	commit(t, d, b)
	check("updated", 1)
	if _, err := d.db.Exec(`DELETE FROM session WHERE id='search'`); err != nil {
		t.Fatal(err)
	}
	check("updated", 0)
	var docs int
	if err := d.db.QueryRow(`SELECT count(*) FROM session_search_fts`).Scan(&docs); err != nil || docs != 0 {
		t.Fatalf("deleted terms remain: %d %v", docs, err)
	}
}

func TestSearchBackfillResumesAndKeepsLiveChanges(t *testing.T) {
	d := openTestDB(t)
	// Model a pre-index database, then apply the migration through Open.
	removeSessionSearchSchema(t, d)
	if _, err := d.db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, migrationIndex(t, searchIndexSchema))); err != nil {
		t.Fatal(err)
	}
	b := Batch{Session: Session{Key: Key{"first", "old"}}, Messages: []Message{{ID: "m", Role: "user", ConversationVisible: true}}}
	for i := range searchBackfillBatch + 2 {
		b.Parts = append(b.Parts, Part{ID: fmt.Sprintf("%03d", i), MessageID: "m", Kind: "text", Text: fmt.Sprintf("fixture searchable %03d", i)})
	}
	commit(t, d, b)
	path := d.FilePath()
	reopen := func() {
		t.Helper()
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		d, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
	}
	reopen()
	if ready, err := d.SessionSearchIndexReady(t.Context()); err != nil || ready {
		t.Fatalf("index unexpectedly ready: %t %v", ready, err)
	}
	if done, err := d.BackfillSessionSearch(t.Context()); err != nil || done {
		t.Fatalf("first batch: %t %v", done, err)
	}
	var docs int
	if err := d.db.QueryRow(`SELECT count(*) FROM session_search_doc`).Scan(&docs); err != nil || docs != searchBackfillBatch {
		t.Fatalf("first batch size: %d %v", docs, err)
	}
	reopen()
	b.ExpectedRevision++
	b.Parts = []Part{{ID: "000", MessageID: "m", Kind: "text", Text: "new text behind the cursor"}}
	commit(t, d, b)
	if done, err := d.BackfillSessionSearch(t.Context()); err != nil || !done {
		t.Fatalf("second batch: %t %v", done, err)
	}
	if done, err := d.BackfillSessionSearch(t.Context()); err != nil || !done {
		t.Fatalf("finished batch: %t %v", done, err)
	}
	for query, want := range map[string]int{"behind the cursor": 1, "searchable 065": 1, "searchable 000": 0} {
		if got, err := d.SearchSessionText(t.Context(), "owner", query); err != nil || len(got) != want {
			t.Fatalf("%q: got %+v, %v; want %d", query, got, err, want)
		}
	}
}

func removeSessionSearchSchema(t *testing.T, d *DB) {
	t.Helper()
	if _, err := d.db.Exec(`DROP TRIGGER session_search_part_insert;
		DROP TRIGGER session_search_part_update; DROP TRIGGER session_search_part_delete;
		DROP TRIGGER session_search_message_update; DROP VIEW session_search_eligible;
		DROP TABLE session_search_doc; DROP TABLE session_search_fts; DROP TABLE session_search_progress;`); err != nil {
		t.Fatal(err)
	}
}
