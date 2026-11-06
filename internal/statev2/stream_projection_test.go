package statev2

import (
	"encoding/json"
	"reflect"
	"strings"
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
		{"title", func(b *StreamBatch) { b.Session.Title = "changed" }, "", streamProjectionChanges{sidebar: true, metadata: true}},
		{"accounting", func(b *StreamBatch) { b.Messages[0].Cost = 1; b.Messages[0].TokensOutput = 42 }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"role", func(b *StreamBatch) { b.Messages[0].Role = "user" }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"visibility", func(b *StreamBatch) { b.Messages[0].ConversationVisible = !b.Messages[0].ConversationVisible }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"completion", func(b *StreamBatch) { b.Messages[0].CompletedAt++ }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"finish", func(b *StreamBatch) { b.Messages[0].Finish = "tool-calls" }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"error", func(b *StreamBatch) { b.Messages[0].HasError = true; b.Messages[0].ErrorName = "failure" }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"source deletion", func(b *StreamBatch) { b.Messages[0].SourceDeleted = true }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"extensions", func(b *StreamBatch) {
			b.Messages[0].Extensions = json.RawMessage(`{"role":"assistant","time":{"completed":100}}`)
		}, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"tool to text", func(b *StreamBatch) { b.Parts[0].Kind = "text" }, "", streamProjectionChanges{analytics: true}},
		{"tool timing", func(b *StreamBatch) {
			b.Parts[0].Extensions = json.RawMessage(`{"tool":"bash","state":{"time":{"start":1,"end":10}}}`)
		}, "", streamProjectionChanges{analytics: true}},
		{"message removal", func(b *StreamBatch) { b.RemovedMessages = []string{"m"} }, "", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
		{"part removal", func(b *StreamBatch) { b.RemovedParts = []PartRef{{MessageID: "m", ID: "p"}} }, "", streamProjectionChanges{analytics: true}},
		{"missing sidebar", func(b *StreamBatch) {}, "session_sidebar", streamProjectionChanges{sidebar: true}},
		{"missing summary", func(b *StreamBatch) {}, "analytics_session", streamProjectionChanges{analytics: true}},
		{"missing attention", func(b *StreamBatch) {}, "sidebar_attention", streamProjectionChanges{attention: true}},
		{"missing compact message", func(b *StreamBatch) {}, "analytics_message", streamProjectionChanges{sidebar: true, analytics: true, attention: true}},
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
	projections := projectionRows(t, d)
	b.Parts[0].Text = "must roll back"
	b.Session.Title = "must roll back"
	b.Session.UpdatedAt++
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
	if after.Parts[0].Text != "old" || after.Session.Revision != 1 || after.Session.Title == "must roll back" {
		t.Fatalf("partial content or revision committed: %+v", after)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "feed"); err != nil || cursor != "1" {
		t.Fatalf("cursor=%q err=%v", cursor, err)
	}
	if after := projectionRows(t, d); !reflect.DeepEqual(projections, after) {
		t.Fatalf("partial projections committed: before=%v after=%v", projections, after)
	}
}

func TestStreamSessionMetadataSkipsAccountingAndAttention(t *testing.T) {
	for _, field := range []string{"title", "directory", "updated", "updated before message", "extensions", "identity", "deleted", "read format"} {
		t.Run(field, func(t *testing.T) {
			d := openTestDB(t)
			s := streamSession("one")
			m := streamMessage("m", 10)
			b := StreamBatch{Session: s, Messages: []Message{m}}
			u := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "feed", Cursor: "1"}, Batches: []StreamBatch{b}}
			if _, err := d.ApplyStream(t.Context(), u); err != nil {
				t.Fatal(err)
			}
			if _, err := d.db.Exec(`CREATE TRIGGER reject_facts BEFORE DELETE ON analytics_fact BEGIN SELECT RAISE(ABORT,'redundant accounting'); END;
			 CREATE TRIGGER reject_attention BEFORE INSERT ON sidebar_attention BEGIN SELECT RAISE(ABORT,'redundant attention'); END;`); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "title":
				s.Title = "renamed"
			case "directory":
				s.Directory = "/new"
			case "updated":
				s.UpdatedAt++
			case "updated before message":
				s.UpdatedAt = 0
			case "extensions":
				s.Extensions = json.RawMessage(`{"SourceChild":true,"ProjectDirectory":"/project","InferredStatus":"error"}`)
			case "identity":
				s.ParentID = "parent"
				s.ProjectID = "project"
				s.Subagent = true
				s.CreatedAt++
			case "deleted":
				s.SourceDeleted = true
			case "read format":
				s.ReadFormat++
			}
			u.Checkpoint = CheckpointUpdate{Stream: "feed", ExpectedCursor: "1", Cursor: "2"}
			if _, err := d.ApplyStream(t.Context(), u); err != nil {
				t.Fatalf("metadata rebuilt accounting or attention: %v", err)
			}
			// Compare all projected values with the full rebuild, not only the
			// changed fields (including updated_at's last-message floor).
			before := projectionRows(t, d)
			if _, err := d.db.Exec(`DROP TRIGGER reject_facts; DROP TRIGGER reject_attention`); err != nil {
				t.Fatal(err)
			}
			tx, err := d.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if err := refreshSidebar(t.Context(), tx, s.Key); err != nil {
				t.Fatal(err)
			}
			if err := rebuildAnalyticsSummary(t.Context(), tx, s.Key); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if after := projectionRows(t, d); !reflect.DeepEqual(before, after) {
				t.Fatalf("incremental projections differ from rebuild:\nbefore=%v\nafter=%v", before, after)
			}
		})
	}
}

func TestStreamProjectionWriteFailuresRemainAtomic(t *testing.T) {
	for _, table := range []string{"session_sidebar", "sidebar_attention", "analytics_session"} {
		t.Run(table, func(t *testing.T) {
			d := openTestDB(t)
			b := StreamBatch{Session: streamSession("one"), Messages: []Message{streamMessage("m", 10)}}
			u := StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "feed", Cursor: "1"}, Batches: []StreamBatch{b}}
			if _, err := d.ApplyStream(t.Context(), u); err != nil {
				t.Fatal(err)
			}
			before := projectionRows(t, d)
			if _, err := d.db.Exec("CREATE TRIGGER reject_projection BEFORE UPDATE ON " + table + " BEGIN SELECT RAISE(ABORT,'injected projection write'); END"); err != nil {
				t.Fatal(err)
			}
			if table == "session_sidebar" {
				tx, err := d.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				err = refreshSidebar(t.Context(), tx, b.Session.Key)
				_ = tx.Rollback()
				if err == nil {
					t.Fatal("full sidebar refresh swallowed a row write failure")
				}
			}
			b.Session.Title = "must roll back"
			b.Session.UpdatedAt++
			if table == "sidebar_attention" {
				b.Messages[0].Cost++ // Require attention as well as accounting refresh.
			}
			u.Checkpoint = CheckpointUpdate{Stream: "feed", ExpectedCursor: "1", Cursor: "2"}
			if _, err := d.ApplyStream(t.Context(), u); err == nil || !strings.Contains(err.Error(), "injected projection write") {
				t.Fatalf("projection failure did not abort the stream: %v", err)
			}
			after, err := d.ReadConversation(t.Context(), b.Session.Key, 30, 0)
			if err != nil {
				t.Fatal(err)
			}
			if after.Session.Revision != 1 || after.Session.Title == "must roll back" || after.Messages[0].Cost != 0 {
				t.Fatalf("partial conversation committed: %+v", after)
			}
			if cursor, err := d.Checkpoint(t.Context(), "first", "feed"); err != nil || cursor != "1" {
				t.Fatalf("cursor=%q err=%v", cursor, err)
			}
			if after := projectionRows(t, d); !reflect.DeepEqual(before, after) {
				t.Fatalf("partial projections committed: before=%v after=%v", before, after)
			}
		})
	}
}

func TestProjectionComparisonFailsClosedWhenInputsCannotBeRead(t *testing.T) {
	for _, input := range []struct{ table, column string }{
		{"session_sidebar", "id"}, {"session", "title"}, {"message", "cost"}, {"part", "kind"},
	} {
		t.Run(input.table, func(t *testing.T) {
			d := openTestDB(t)
			b := StreamBatch{Session: streamSession("one"), Messages: []Message{streamMessage("m", 10)}, Parts: []Part{{ID: "p", MessageID: "m", Kind: "text"}}}
			if _, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "feed", Cursor: "1"}, Batches: []StreamBatch{b}}); err != nil {
				t.Fatal(err)
			}
			tx, err := d.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			// Simulate unreadable comparison inputs only inside this temporary
			// transaction; a failed read must not classify facts as unchanged.
			if _, err := tx.Exec("ALTER TABLE " + input.table + " RENAME COLUMN " + input.column + " TO unavailable_input"); err != nil {
				t.Fatal(err)
			}
			if _, err := projectionChanges(t.Context(), tx, b); err == nil {
				t.Fatal("unreadable projection inputs were accepted")
			}
		})
	}
}

func projectionRows(t *testing.T, d *DB) []string {
	t.Helper()
	var result []string
	for _, table := range []string{"session_sidebar", "sidebar_attention", "analytics_session", "analytics_fact", "analytics_hour", "analytics_question"} {
		rows, err := d.db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, table+string(raw))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
