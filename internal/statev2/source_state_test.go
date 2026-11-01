package statev2

import (
	"errors"
	"maps"
	"testing"
)

func TestUsageMatchesToleratesOnlySummationOrder(t *testing.T) {
	want := Usage{Messages: 2, Cost: 0.3, TokensInput: 5}
	// Runtime operands: the constant expression 0.1 + 0.2 is exact and equals 0.3.
	a, b := 0.1, 0.2
	summed := a + b
	if summed == want.Cost {
		t.Fatal("fixture no longer exercises a rounding difference")
	}
	if !want.Matches(Usage{Messages: 2, Cost: summed, TokensInput: 5}) {
		t.Fatal("summation order rejected")
	}
	for name, got := range map[string]Usage{
		"message count": {Messages: 1, Cost: 0.3, TokensInput: 5},
		"cost":          {Messages: 2, Cost: 0.31, TokensInput: 5},
		"tokens":        {Messages: 2, Cost: 0.3, TokensInput: 6},
		"cache":         {Messages: 2, Cost: 0.3, TokensInput: 5, TokensCacheWrite: 1},
	} {
		if want.Matches(got) {
			t.Fatalf("%s mismatch accepted", name)
		}
	}
}

func receiptUpdate(verify *Usage) *SourceUpdate {
	return &SourceUpdate{
		State:    map[string]string{"read-ready/session": "", "hint": "always"},
		Verify:   verify,
		Verified: map[string]string{"read-ready/session": "v1"},
		Streams:  map[string]string{"seq/session": "v1"},
	}
}

func sourceState(t *testing.T, d *DB) map[string]string {
	t.Helper()
	state, err := d.SourceState(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// Bookkeeping commits with the content or not at all, and verification
// runs against the content written in the same transaction.
func TestCommitAppliesSourceUpdateWithContent(t *testing.T) {
	d := openTestDB(t)
	conflicting := fixture("first")
	conflicting.ExpectedRevision = 3
	conflicting.Source = receiptUpdate(nil)
	if _, err := d.Commit(t.Context(), conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	if state := sourceState(t, d); len(state) != 0 {
		t.Fatalf("rolled-back commit left source state %v", state)
	}

	b := fixture("first")
	b.Source = receiptUpdate(&Usage{Messages: 1})
	if r := commit(t, d, b); r.UsageMismatch {
		t.Fatal("matching usage reported a mismatch")
	}
	if state := sourceState(t, d); !maps.Equal(state, map[string]string{"read-ready/session": "v1", "hint": "always"}) {
		t.Fatalf("source state = %v", state)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "seq/session"); err != nil || cursor != "v1" {
		t.Fatalf("verified stream = %q, %v", cursor, err)
	}

	// A duplicate checkpoint still records bookkeeping; a mismatch withholds
	// the verified receipt and stream while clearing the old receipt.
	b.ExpectedRevision = 1
	b.Source = receiptUpdate(&Usage{Messages: 2})
	b.Source.Streams = map[string]string{"seq/session": "v2"}
	if r := commit(t, d, b); !r.Duplicate || !r.UsageMismatch {
		t.Fatalf("duplicate mismatch result = %+v", r)
	}
	if state := sourceState(t, d); !maps.Equal(state, map[string]string{"hint": "always"}) {
		t.Fatalf("mismatch kept receipt: %v", state)
	}
	if cursor, _ := d.Checkpoint(t.Context(), "first", "seq/session"); cursor != "v1" {
		t.Fatalf("mismatch advanced stream to %q", cursor)
	}
}

func TestApplySourceAndStreamSourceState(t *testing.T) {
	d := openTestDB(t)
	commit(t, d, fixture("first"))
	key := Key{InstanceID: "first", SessionID: "session"}
	if mismatch, err := d.ApplySource(t.Context(), key, *receiptUpdate(&Usage{Messages: 1})); err != nil || mismatch {
		t.Fatalf("ApplySource = %v, %v", mismatch, err)
	}
	if state := sourceState(t, d); state["read-ready/session"] != "v1" {
		t.Fatalf("source state = %v", state)
	}
	if _, err := d.ApplySource(t.Context(), key, SourceUpdate{State: map[string]string{"": "x"}}); err == nil {
		t.Fatal("empty source key accepted")
	}
	if _, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first",
		Checkpoint:  CheckpointUpdate{Stream: "seq/session", ExpectedCursor: "v1", Cursor: "v2"},
		SourceState: map[string]string{"read-ready/session": "v2"}}); err != nil {
		t.Fatal(err)
	}
	if state := sourceState(t, d); state["read-ready/session"] != "v2" {
		t.Fatalf("stream receipt = %v", state)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplySource(t.Context(), key, SourceUpdate{}); err == nil {
		t.Fatal("closed store accepted source state")
	}
}
