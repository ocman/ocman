package statev2

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestContextProjectionUpgradesStoredHistory(t *testing.T) {
	for _, payload := range []string{
		`{"role":"assistant","contextTokens":12000,"tokens":{"input":100000,"output":500}}`,
		`{"metadata":{"role":"assistant","contextTokens":12000,"tokens":{"input":100000,"output":500}},"native":{}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.db")
			d, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			register(t, d, "first")
			b := fixture("first")
			b.Messages[0].ConversationVisible = true
			b.Messages[0].Extensions = json.RawMessage(payload)
			commit(t, d, b)
			// Emulate the shipped projection that omitted current context.
			removeBackgroundSchema(t, d)
			if _, err := d.db.Exec(`UPDATE analytics_message SET data=json_remove(data,'$.contextTokens'); PRAGMA user_version=15`); err != nil {
				t.Fatal(err)
			}
			d.Close()
			d, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			page, err := d.ReadConversation(t.Context(), b.Session.Key, 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			if page.Conversation.ContextTokens != 12000 {
				t.Fatalf("upgraded context = %d, want 12000", page.Conversation.ContextTokens)
			}
			if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
				t.Fatalf("upgrade changed checkpoint: %q, %v", cursor, err)
			}
		})
	}
}
