package statev2

import (
	"errors"
	"reflect"
	"testing"
)

func TestCommitObserverSeesOnlyCommittedRevisions(t *testing.T) {
	d := openTestDB(t)
	var seen []string
	d.OnCommitted(func(key Key, revision int64) { seen = append(seen, key.SessionID+"@"+string(rune('0'+revision))) })

	b := fixture("first")
	commit(t, d, b)
	// A duplicate checkpoint commits nothing.
	if r, err := d.Commit(t.Context(), b); err != nil || !r.Duplicate {
		t.Fatalf("duplicate = %+v, %v", r, err)
	}
	update := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "journal/a", Cursor: "1"},
		Batches: []StreamBatch{{Key: b.Session.Key, Messages: []Message{streamMessage("next", 30)}}}}
	if _, err := d.ApplyStream(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	// A conflicting update rolls back and is not reported.
	update.Checkpoint = CheckpointUpdate{Stream: "journal/a", ExpectedCursor: "0", Cursor: "2"}
	if _, err := d.ApplyStream(t.Context(), update); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	if want := []string{"session@1", "session@2"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("observed %v, want %v", seen, want)
	}
	d.OnCommitted(nil)
	update.Checkpoint = CheckpointUpdate{Stream: "journal/a", ExpectedCursor: "1", Cursor: "3"}
	if _, err := d.ApplyStream(t.Context(), update); err != nil || len(seen) != 2 {
		t.Fatalf("removed observer still notified: %v %v", seen, err)
	}
}
