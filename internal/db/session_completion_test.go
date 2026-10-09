package db

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func TestV2SyntheticMessageDoesNotAdvanceUserPromptTimestamp(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	path := writeV2DB(t)
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Exec(`INSERT INTO session_message VALUES
		('msg_synthetic', 'ses_a', 'synthetic', 3, 100, 100,
		'{"text":"automatic follow-up","time":{"created":100}}')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	row, err := d.GetSessionSummary(t.Context(), "ses_a")
	if err != nil {
		t.Fatal(err)
	}
	if row.LastUserPromptAt != 1 {
		t.Fatalf("last user prompt = %d, want 1 before synthetic message", row.LastUserPromptAt)
	}
}

func completionTimestamp(t *testing.T, session any) int64 {
	t.Helper()
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		LastTurnCompletedAt int64 `json:"lastTurnCompletedAt"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields.LastTurnCompletedAt
}

func TestSessionCompletionTimestamp(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	insertSession(t, d, "session", "Work", "/repo", 1, 999)
	check := func(want int64) {
		t.Helper()
		list, err := d.GetSessions(t.Context(), "", 0)
		if err != nil || len(list) != 1 {
			t.Fatalf("list = %v, error = %v", list, err)
		}
		summary, err := d.GetSessionSummary(t.Context(), "session")
		if err != nil {
			t.Fatal(err)
		}
		tree, err := d.GetSessionTree(t.Context(), "session")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []any{list[0], summary, tree[0]} {
			if got := completionTimestamp(t, row); got != want {
				t.Fatalf("completion = %d, want %d", got, want)
			}
		}
	}
	check(0)
	insertMessage(t, d, "final", "session", 10, map[string]any{
		"role": "assistant", "finish": "stop", "time": map[string]any{"completed": 20},
	})
	check(20)
	for i, data := range []map[string]any{
		{"role": "user"},
		{"role": "assistant", "time": map[string]any{"completed": 40}, "finish": "tool-calls"},
		{"role": "assistant", "time": map[string]any{"completed": 50}, "finish": "unknown"},
		{"role": "assistant", "time": map[string]any{"completed": 60}, "finish": "stop", "summary": true},
		{"role": "assistant"},
	} {
		insertMessage(t, d, string(rune('a'+i)), "session", int64(30+i*10), data)
		check(20)
	}
	insertMessage(t, d, "error", "session", 80, map[string]any{
		"role": "assistant", "error": map[string]any{"name": "APIError"},
	})
	check(80)
	insertMessage(t, d, "error-finish-only", "session", 85, map[string]any{
		"role": "assistant", "finish": "error",
	})
	check(85)
	insertMessage(t, d, "next-final", "session", 90, map[string]any{
		"role": "assistant", "finish": "length", "time": map[string]any{"completed": 100},
	})
	check(100)
}

func TestSessionMetadataLookupDoesNotParseMessages(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	insertSession(t, d, "metadata", "Metadata", "/repo", 1, 999)
	if _, err := d.db.Exec(`INSERT INTO message (id, session_id, data) VALUES ('invalid', 'metadata', 'not-json')`); err != nil {
		t.Fatal(err)
	}
	row, err := d.GetSession(t.Context(), "metadata")
	if err != nil {
		t.Fatalf("metadata lookup inspected message JSON: %v", err)
	}
	if row.ID != "metadata" || row.Directory != "/repo" || row.TimeUpdated != 999 {
		t.Fatalf("metadata lookup = %+v", row)
	}
}

func TestSessionCompletionTimestampV2(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	d, err := Open(writeV2DB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	row, err := d.GetSessionSummary(t.Context(), "ses_a")
	if err != nil {
		t.Fatal(err)
	}
	if got := completionTimestamp(t, row); got != 3 {
		t.Fatalf("v2 completion = %d, want 3", got)
	}
}

func TestLastUserPromptTimestamp(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	insertSession(t, d, "prompted", "Work", "/repo", 1, 999)
	insertMessage(t, d, "user-old", "prompted", 10, map[string]any{"role": "user"})
	insertMessage(t, d, "user-new", "prompted", 21, map[string]any{"role": "user"})
	insertMessage(t, d, "assistant-stream", "prompted", 30, map[string]any{"role": "assistant"})
	list, err := d.GetSessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := d.GetSessionSummary(t.Context(), "prompted")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := d.GetSessionTree(t.Context(), "prompted")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []Session{list[0], summary, tree[0]} {
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			LastUserPromptAt int64 `json:"lastUserPromptAt"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if fields.LastUserPromptAt != 21 {
			t.Fatalf("last user prompt = %d, want 21", fields.LastUserPromptAt)
		}
	}
}
