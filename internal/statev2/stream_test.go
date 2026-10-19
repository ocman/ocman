package statev2

import (
	"encoding/json"
	"errors"
	"testing"
)

func streamSession(id string) *Session {
	return &Session{Key: Key{InstanceID: "first", SessionID: id}, Title: id, Directory: "/repo", CreatedAt: 1, UpdatedAt: 2,
		Extensions: json.RawMessage(`{}`), ReadFormat: ConversationReadFormat}
}

func streamMessage(id string, created int64) Message {
	return Message{ID: id, Role: "assistant", CreatedAt: created, ConversationVisible: true,
		Extensions: json.RawMessage(`{"role":"assistant","finish":"stop"}`)}
}

func TestApplyStreamCommitsSessionsAndCursorAtomically(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "first")
	update := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "journal/opencode/a", Cursor: "100"}, Batches: []StreamBatch{
		{Session: streamSession("one"), Messages: []Message{streamMessage("m1", 10)}, Parts: []Part{{ID: "p1", MessageID: "m1", Kind: "text", Text: "first", TextPresent: true}}},
		{Session: streamSession("two"), Messages: []Message{streamMessage("other", 10)}},
	}}
	result, err := d.ApplyStream(t.Context(), update)
	if err != nil || result.Duplicate || len(result.Sessions) != 2 {
		t.Fatalf("first apply = %+v, %v", result, err)
	}
	// Replaying a committed cursor must be harmless, including after restart.
	if replay, err := d.ApplyStream(t.Context(), update); err != nil || !replay.Duplicate {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	// An event without metadata keeps existing session fields, appends a new
	// message after the existing one and never marks unseen content deleted.
	next := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "journal/opencode/a", ExpectedCursor: "100", Cursor: "200"}, Batches: []StreamBatch{
		{Key: Key{InstanceID: "first", SessionID: "one"}, Messages: []Message{streamMessage("m2", 5)},
			Parts: []Part{{ID: "p2", MessageID: "m2", Kind: "text", Text: "second", TextPresent: true}, {ID: "p1b", MessageID: "m1", Kind: "text", Text: "more", TextPresent: true}}},
	}}
	if _, err := d.ApplyStream(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	page, err := d.ReadConversation(t.Context(), Key{InstanceID: "first", SessionID: "one"}, 30, 0)
	if err != nil || page.Session.Title != "one" || len(page.Messages) != 2 || page.Messages[0].ID != "m1" || page.Messages[1].ID != "m2" {
		t.Fatalf("stream order or metadata changed: %+v %v", page, err)
	}
	if len(page.Parts) != 3 || page.Parts[0].ID != "p1" || page.Parts[1].ID != "p1b" || page.Parts[2].ID != "p2" {
		t.Fatalf("parts were not appended in event order: %+v", page.Parts)
	}
	// A stale predecessor is a cursor conflict and must not apply anything.
	stale := next
	stale.Checkpoint = CheckpointUpdate{Stream: "journal/opencode/a", ExpectedCursor: "100", Cursor: "300"}
	stale.Batches = []StreamBatch{{Key: Key{InstanceID: "first", SessionID: "one"}, Messages: []Message{streamMessage("lost", 20)}}}
	if _, err := d.ApplyStream(t.Context(), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale cursor = %v", err)
	}
	if page, _ := d.ReadConversation(t.Context(), Key{InstanceID: "first", SessionID: "one"}, 30, 0); page.TotalMessages != 2 {
		t.Fatal("conflicting stream update was partially applied")
	}
}

func TestApplyStreamRollsBackEverySessionOnFailure(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "first")
	bad := Message{ID: "bad", Role: "assistant", Extensions: json.RawMessage(`{`)}
	_, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "journal/x", Cursor: "1"}, Batches: []StreamBatch{
		{Session: streamSession("good"), Messages: []Message{streamMessage("ok", 1)}},
		{Session: streamSession("broken"), Messages: []Message{bad}},
	}})
	if err == nil {
		t.Fatal("accepted invalid content")
	}
	if cursor, _ := d.Checkpoint(t.Context(), "first", "journal/x"); cursor != "" {
		t.Fatal("cursor advanced past a failed update")
	}
	if _, err := d.ReadConversation(t.Context(), Key{InstanceID: "first", SessionID: "good"}, 30, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("an earlier session committed without the rest of the stream update", err)
	}
}

func TestApplyStreamRemovalsPurgesAndUnknownSessions(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "first")
	key := Key{InstanceID: "first", SessionID: "one"}
	if _, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", Cursor: "1"}, Batches: []StreamBatch{
		{Session: streamSession("one"), Messages: []Message{streamMessage("m1", 1), streamMessage("m2", 2)}, Parts: []Part{{ID: "p", MessageID: "m1", Kind: "text", Text: "x", TextPresent: true}}},
	}}); err != nil {
		t.Fatal(err)
	}
	// Removal is retained as source-deleted audit history, never a delete.
	if _, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", ExpectedCursor: "1", Cursor: "2"}, Batches: []StreamBatch{
		{Key: key, RemovedMessages: []string{"m2"}, RemovedParts: []PartRef{{MessageID: "m1", ID: "p"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	page, _ := d.ReadConversation(t.Context(), key, 30, 0)
	audit, _ := d.Read(t.Context(), key, 30, 0)
	if page.TotalMessages != 1 || len(page.Parts) != 0 || audit.TotalMessages != 2 || len(audit.Parts) != 1 {
		t.Fatalf("removal was not retained as history: current=%+v audit=%+v", page, audit)
	}
	if err := d.Purge(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	result, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", ExpectedCursor: "2", Cursor: "3"}, Batches: []StreamBatch{
		{Session: streamSession("one"), Messages: []Message{streamMessage("resurrected", 3)}},
	}})
	if err != nil || len(result.Suppressed) != 1 {
		t.Fatalf("purge was not suppressed: %+v %v", result, err)
	}
	if _, err := d.ReadConversation(t.Context(), key, 30, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("stream event resurrected purged history")
	}
	// Events for a session whose metadata was never captured cannot invent a
	// placeholder; the consumer must supply it from the source first.
	_, err = d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", ExpectedCursor: "3", Cursor: "4"}, Batches: []StreamBatch{
		{Key: Key{InstanceID: "first", SessionID: "unknown"}, Messages: []Message{streamMessage("m", 1)}},
	}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session metadata = %v", err)
	}
}

func TestCheckpointsReadsOnlyMatchingStreams(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "first")
	register(t, d, "second")
	for _, u := range []StreamUpdate{
		{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "seq/a", Cursor: "seq:1"}},
		{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "seq/b_%", Cursor: "seq:2"}},
		{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "journal/x", Cursor: "9"}},
		{InstanceID: "second", Checkpoint: CheckpointUpdate{Stream: "seq/a", Cursor: "seq:7"}},
	} {
		if _, err := d.ApplyStream(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.Checkpoints(t.Context(), "first", "seq/")
	if err != nil || len(got) != 2 || got["a"] != "seq:1" || got["b_%"] != "seq:2" {
		t.Fatalf("checkpoints = %v, %v", got, err)
	}
	if none, err := d.Checkpoints(t.Context(), "first", "seq_"); err != nil || len(none) != 0 {
		t.Fatalf("prefix wildcard matched: %v %v", none, err)
	}
}

func TestApplyStreamRejectsInvalidUpdates(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "first")
	for name, update := range map[string]StreamUpdate{
		"no stream":        {InstanceID: "first", Checkpoint: CheckpointUpdate{Cursor: "1"}},
		"unchanged cursor": {InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", ExpectedCursor: "1", Cursor: "1"}},
		"other instance":   {InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", Cursor: "1"}, Batches: []StreamBatch{{Session: &Session{Key: Key{InstanceID: "other", SessionID: "x"}}}}},
		"no identity":      {InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "s", Cursor: "1"}, Batches: []StreamBatch{{}}},
	} {
		if _, err := d.ApplyStream(t.Context(), update); err == nil {
			t.Errorf("%s: accepted invalid stream update", name)
		}
	}
}
