package db

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func v2FeedDB(t *testing.T) *DB {
	t.Helper()
	path := writeV2DB(t)
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`CREATE TABLE event_sequence (aggregate_id TEXT PRIMARY KEY, seq INTEGER NOT NULL);
		INSERT INTO event_sequence VALUES ('ses_a', 12), ('ses_b', 3), ('per_x', 9);`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	restore := ocv2.SetInstalledV2(true)
	t.Cleanup(restore)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestHistorySequencesListOnlySessions(t *testing.T) {
	d := v2FeedDB(t)
	seqs, err := d.HistorySequences(t.Context())
	if err != nil || len(seqs) != 2 || seqs["ses_a"] != 12 || seqs["ses_b"] != 3 {
		t.Fatalf("sequences = %v, %v", seqs, err)
	}
	for id, want := range map[string]string{"ses_a": "seq:12", "ses_new": "seq:none"} {
		if got, err := d.HistorySequence(t.Context(), id); err != nil || got != want {
			t.Fatalf("%s version = %q, %v; want %q", id, got, err, want)
		}
	}
	// v1 has no durable event sequence: callers must fall back explicitly.
	v1 := &DB{db: d.db}
	if _, err := v1.HistorySequences(t.Context()); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("v1 sequences = %v", err)
	}
	if _, err := v1.HistorySequence(t.Context(), "ses_a"); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("v1 sequence = %v", err)
	}
	if _, err := v1.HistoryMessages(t.Context(), "ses_a", nil); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatalf("v1 messages = %v", err)
	}
}

func TestHistoryMessagesReadsOnlyRequestedMessages(t *testing.T) {
	d := v2FeedDB(t)
	snap, err := d.HistoryMessages(t.Context(), "ses_a", []string{"msg_0003", "msg_missing"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Session.ID != "ses_a" || snap.Session.Title != "Titled" || len(snap.Messages) != 1 || snap.Messages[0].ID != "msg_0003" {
		t.Fatalf("messages = %+v", snap)
	}
	// The selected message converts exactly as in a full snapshot.
	full, err := d.HistorySnapshot(t.Context(), "ses_a")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, p := range full.Parts {
		if p.MessageID == "msg_0003" {
			want = append(want, p.ID+string(p.Data))
		}
	}
	var got []string
	for _, p := range snap.Parts {
		got = append(got, p.ID+string(p.Data))
	}
	if len(got) == 0 || strings.Join(got, "\n") != strings.Join(want, "\n") || string(snap.NativeMessages["msg_0003"]) != string(full.NativeMessages["msg_0003"]) {
		t.Fatalf("partial read differs from snapshot:\n%v\n%v", got, want)
	}
	// Session-only reads return metadata without content.
	meta, err := d.HistoryMessages(t.Context(), "ses_a", nil)
	if err != nil || meta.Session.ID != "ses_a" || len(meta.Messages) != 0 {
		t.Fatalf("metadata = %+v, %v", meta, err)
	}
	if _, err := d.HistoryMessages(t.Context(), "missing", nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing session = %v", err)
	}
}

func TestHistoryMessagesMarksSubagentTitles(t *testing.T) {
	path := writeV2DB(t)
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`UPDATE session_v2 SET title='Explore (@explore subagent)' WHERE id='ses_b'`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	t.Cleanup(ocv2.SetInstalledV2(true))
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for id, want := range map[string]bool{"ses_a": false, "ses_b": true} {
		snap, err := d.HistoryMessages(t.Context(), id, nil)
		if err != nil || snap.Subagent != want {
			t.Fatalf("%s subagent = %+v, %v; want %v", id, snap, err, want)
		}
	}
}
