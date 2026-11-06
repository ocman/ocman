package statev2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProjectionChangesRetainMetadataAccountingAndRemovalWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*StreamBatch)
		remove string
		want   streamProjectionChanges
	}{
		{"unchanged", func(b *StreamBatch) {}, "", streamProjectionChanges{}},
		{"revision and position are owned locally", func(b *StreamBatch) { b.Session.Revision = 999; b.Messages[0].Position = 999 }, "", streamProjectionChanges{}},
		{"title", func(b *StreamBatch) { b.Session.Title = "changed" }, "", streamProjectionChanges{true, true}},
		{"accounting", func(b *StreamBatch) { b.Messages[0].Cost = 1; b.Messages[0].TokensOutput = 42 }, "", streamProjectionChanges{true, true}},
		{"extensions", func(b *StreamBatch) {
			b.Messages[0].Extensions = json.RawMessage(`{"role":"assistant","time":{"completed":100}}`)
		}, "", streamProjectionChanges{true, true}},
		{"tool to text", func(b *StreamBatch) { b.Parts[0].Kind = "text" }, "", streamProjectionChanges{false, true}},
		{"tool timing", func(b *StreamBatch) {
			b.Parts[0].Extensions = json.RawMessage(`{"tool":"bash","state":{"time":{"start":1,"end":10}}}`)
		}, "", streamProjectionChanges{false, true}},
		{"message removal", func(b *StreamBatch) { b.RemovedMessages = []string{"m"} }, "", streamProjectionChanges{true, true}},
		{"part removal", func(b *StreamBatch) { b.RemovedParts = []PartRef{{MessageID: "m", ID: "p"}} }, "", streamProjectionChanges{false, true}},
		{"missing sidebar", func(b *StreamBatch) {}, "session_sidebar", streamProjectionChanges{true, false}},
		{"missing summary", func(b *StreamBatch) {}, "analytics_session", streamProjectionChanges{false, true}},
		{"missing compact message", func(b *StreamBatch) {}, "analytics_message", streamProjectionChanges{true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			b := StreamBatch{Session: streamSession("one"), Messages: []Message{streamMessage("m", 10)}, Parts: []Part{{ID: "p", MessageID: "m", Kind: "tool", Extensions: json.RawMessage(`{"tool":"bash"}`)}}}
			if _, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "feed", Cursor: "1"}, Batches: []StreamBatch{b}}); err != nil {
				t.Fatal(err)
			}
			// Unchanged text must skip analytics; tool-related cases start with the
			// persisted tool and therefore must not be classified as text-only.
			if tc.name != "tool to text" && tc.name != "tool timing" {
				if _, err := d.db.Exec(`UPDATE part SET kind='text'; DELETE FROM analytics_part`); err != nil {
					t.Fatal(err)
				}
				b.Parts[0].Kind = "text"
			}
			if tc.remove != "" {
				if _, err := d.db.Exec("DELETE FROM " + tc.remove); err != nil {
					t.Fatal(err)
				}
			}
			tc.mutate(&b)
			tx, err := d.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			got, err := projectionChanges(t.Context(), tx, b)
			if err != nil || got != tc.want {
				t.Fatalf("changes=%+v want=%+v err=%v", got, tc.want, err)
			}
		})
	}
}

func TestTextDeltaSkipsUnchangedProjectionsWithFeedMetadata(t *testing.T) {
	d := openTestDB(t)
	s := streamSession("one")
	m := streamMessage("m", 10)
	part := Part{ID: "p", MessageID: "m", Kind: "text", Text: "old", TextPresent: true}
	update := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "feed", Cursor: "1"},
		Batches: []StreamBatch{{Session: s, Messages: []Message{m}, Parts: []Part{part}}}}
	if _, err := d.ApplyStream(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	before, err := d.ReadConversation(t.Context(), s.Key, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A real feed carries unchanged session/message metadata with its text.
	// Make needless derived work observable without wall-clock thresholds.
	if _, err := d.db.Exec(`CREATE TRIGGER reject_sidebar BEFORE INSERT ON session_sidebar BEGIN SELECT RAISE(ABORT,'redundant sidebar'); END;
		CREATE TRIGGER reject_analytics BEFORE INSERT ON analytics_session BEGIN SELECT RAISE(ABORT,'redundant analytics'); END;`); err != nil {
		t.Fatal(err)
	}
	part.Text = "new"
	update.Checkpoint = CheckpointUpdate{Stream: "feed", ExpectedCursor: "1", Cursor: "2"}
	update.Batches[0].Parts = []Part{part}
	if _, err := d.ApplyStream(t.Context(), update); err != nil {
		t.Fatalf("text-only delta rebuilt unchanged projections: %v", err)
	}
	after, err := d.ReadConversation(t.Context(), s.Key, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.Parts[0].Text != "new" || after.Session.Revision != before.Session.Revision+1 || !reflect.DeepEqual(before.ConversationTotals, after.ConversationTotals) {
		t.Fatalf("content, revision, or accounting changed incorrectly: %+v", after)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "feed"); err != nil || cursor != "2" {
		t.Fatalf("cursor=%q err=%v", cursor, err)
	}
}

func TestSkippedProjectionTextUpdateRollsBackWithLaterFailure(t *testing.T) {
	d := openTestDB(t)
	b := StreamBatch{Session: streamSession("one"), Messages: []Message{streamMessage("m", 10)}, Parts: []Part{{ID: "p", MessageID: "m", Kind: "text", Text: "old", TextPresent: true}}}
	update := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "feed", Cursor: "1"}, Batches: []StreamBatch{b}}
	if _, err := d.ApplyStream(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	b.Parts[0].Text = "must roll back"
	bad := streamMessage("bad", 20)
	bad.Extensions = json.RawMessage(`{`)
	update.Checkpoint = CheckpointUpdate{Stream: "feed", ExpectedCursor: "1", Cursor: "2"}
	update.Batches = []StreamBatch{b, {Session: streamSession("broken"), Messages: []Message{bad}}}
	if _, err := d.ApplyStream(t.Context(), update); err == nil {
		t.Fatal("invalid later session committed")
	}
	after, err := d.ReadConversation(t.Context(), b.Session.Key, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.Parts[0].Text != "old" || after.Session.Revision != 1 {
		t.Fatalf("partial content or revision committed: %+v", after)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "feed"); err != nil || cursor != "1" {
		t.Fatalf("cursor=%q err=%v", cursor, err)
	}
}
