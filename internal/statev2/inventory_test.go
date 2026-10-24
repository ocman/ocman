package statev2

import "testing"

func TestIngestionInventoryAndCompleteSnapshots(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	state, err := d.SessionState(t.Context(), b.Session.Key)
	if err != nil || state.Revision != 0 {
		t.Fatalf("missing state: %+v %v", state, err)
	}
	commit(t, d, b)
	state, err = d.SessionState(t.Context(), b.Session.Key)
	if err != nil || state.Title != b.Session.Title || state.Revision != 1 {
		t.Fatalf("stored state: %+v %v", state, err)
	}
	sessions, err := d.Sessions(t.Context(), "first")
	if err != nil || len(sessions) != 1 || sessions[0].Revision != 1 {
		t.Fatalf("inventory: %+v %v", sessions, err)
	}
	b.ExpectedRevision, b.Checkpoint, b.Complete = 1, nil, true
	b.Messages, b.Parts = nil, nil
	commit(t, d, b)
	got := read(t, d, b.Session.Key)
	if !got.Messages[0].SourceDeleted || !got.Parts[0].SourceDeleted || got.Parts[0].Text != "retained content" {
		t.Fatal("snapshot removed retained content")
	}
	b = fixture("first")
	b.ExpectedRevision, b.Checkpoint, b.Complete = 2, nil, true
	commit(t, d, b)
	got = read(t, d, b.Session.Key)
	if got.Messages[0].SourceDeleted || got.Parts[0].SourceDeleted {
		t.Fatal("snapshot did not restore present content")
	}
}

func TestIncompleteStorageDoesNotAuthorizeSnapshotDeletion(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	commit(t, d, b)
	if _, err := d.db.Exec("DROP TABLE part"); err != nil {
		t.Fatal(err)
	}
	b.ExpectedRevision, b.Checkpoint, b.Complete = 1, nil, true
	if _, err := d.Commit(t.Context(), b); err == nil {
		t.Fatal("committed an incomplete snapshot")
	}
	var deleted bool
	if err := d.db.QueryRow(`SELECT source_deleted FROM message`).Scan(&deleted); err != nil || deleted {
		t.Fatalf("failed snapshot changed message flags: %v %v", deleted, err)
	}
	d.Close()
	if _, err := d.SessionState(t.Context(), b.Session.Key); err == nil {
		t.Fatal("read closed purge state")
	}
	if _, err := d.Sessions(t.Context(), "first"); err == nil {
		t.Fatal("read closed inventory")
	}
}
