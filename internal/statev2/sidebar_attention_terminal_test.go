package statev2

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAttentionIgnoresNonterminalAssistantSteps(t *testing.T) {
	for _, tc := range []struct {
		name, finish, extensions string
		completed, want          int64
		hasError                 bool
	}{
		{name: "tool step", finish: "tool-calls", completed: 300, want: 120},
		{name: "unknown step", finish: "unknown", completed: 300, want: 120},
		{name: "unfinished step", completed: 300, want: 120},
		{name: "still streaming", finish: "stop", want: 120},
		{name: "summary", finish: "stop", completed: 300, extensions: `{"summary":true}`, want: 120},
		{name: "native summary", finish: "stop", completed: 300, extensions: `{"metadata":{"summary":true}}`, want: 120},
		{name: "completed turn", finish: "stop", completed: 300, want: 300},
		{name: "length limit", finish: "length", completed: 300, want: 300},
		{name: "error envelope", hasError: true, want: 250},
		{name: "error finish", finish: "error", want: 250},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			b := fixture("first")
			b.Parts, b.Checkpoint = nil, nil
			b.Session.ReadFormat = ConversationReadFormat
			b.Messages = []Message{
				{ID: "prompt", Role: "user", CreatedAt: 100, ConversationVisible: true},
				{ID: "previous", Role: "assistant", CreatedAt: 110, CompletedAt: 120, Finish: "stop", ConversationVisible: true, Position: 1},
			}
			commit(t, d, b)
			ext := tc.extensions
			if ext == "" {
				ext = `{}`
			}
			_, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "events", Cursor: "1"}, Batches: []StreamBatch{{
				Session: &b.Session,
				Messages: []Message{{ID: "step", Role: "assistant", CreatedAt: 250, CompletedAt: tc.completed, Finish: tc.finish,
					HasError: tc.hasError, ConversationVisible: true, Position: 2, Extensions: json.RawMessage(ext)}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			assertAttention(t, d, tc.want, 100)
			page, err := d.ReadConversation(t.Context(), b.Session.Key, 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			if page.Conversation.LastTurnCompletedAt != tc.want || page.Conversation.LastUserPromptAt != 100 {
				t.Fatalf("conversation attention=%d/%d want=%d/100", page.Conversation.LastTurnCompletedAt, page.Conversation.LastUserPromptAt, tc.want)
			}
		})
	}
}

func TestAttentionTerminalMigrationRepairsExistingTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	register(t, d, "first")
	b := fixture("first")
	b.Parts = nil
	b.Messages = []Message{
		{ID: "prompt", Role: "user", CreatedAt: 100, ConversationVisible: true},
		{ID: "terminal", Role: "assistant", CreatedAt: 110, CompletedAt: 120, Finish: "stop", ConversationVisible: true, Position: 1},
		{ID: "step", Role: "assistant", CreatedAt: 250, CompletedAt: 300, Finish: "tool-calls", ConversationVisible: true, Position: 2},
	}
	commit(t, d, b)
	// Emulate the shipped v17 projection without modifying retained messages.
	if _, err := d.db.Exec(`UPDATE sidebar_attention SET last_turn_completed_at=300; PRAGMA user_version=17`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	assertAttention(t, d, 120, 100)
	if got := read(t, d, b.Session.Key); got.TotalMessages != 3 || got.Session.Revision != 1 {
		t.Fatalf("migration changed retained history: %+v", got)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatalf("migration changed checkpoint: %q %v", cursor, err)
	}
}

func TestAttentionToolStepsDoNotCreateFirstCompletion(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Parts = nil
	b.Messages = []Message{
		{ID: "prompt", Role: "user", CreatedAt: 100, ConversationVisible: true},
		{ID: "step", Role: "assistant", CreatedAt: 250, CompletedAt: 300, Finish: "tool-calls", ConversationVisible: true, Position: 1},
	}
	commit(t, d, b)
	assertAttention(t, d, 0, 100)
}

func assertAttention(t *testing.T, d *DB, completed, prompt int64) {
	t.Helper()
	var gotCompleted, gotPrompt int64
	if err := d.db.QueryRow(`SELECT last_turn_completed_at,last_user_prompt_at FROM sidebar_attention`).Scan(&gotCompleted, &gotPrompt); err != nil || gotCompleted != completed || gotPrompt != prompt {
		t.Fatalf("attention=%d/%d want=%d/%d: %v", gotCompleted, gotPrompt, completed, prompt, err)
	}
}
