package db

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestDescendantMessageMetadata(t *testing.T) {
	database := openTestDB(t)
	defer database.Close()
	for _, query := range []string{
		`INSERT INTO session(id, parent_id) VALUES ('root', 'parent'), ('child', 'root'), ('deep', 'child'), ('parent', NULL), ('sibling', 'parent'), ('empty', NULL)`,
		`INSERT INTO message(id, session_id, data) VALUES ('r', 'root', '{"role":"assistant","providerID":"p","modelID":"m","cost":1,"tokens":{"input":10},"summary":{"diffs":"large patch"},"text":"not usage"}'), ('c', 'child', '{}'), ('d', 'deep', 'invalid'), ('p', 'parent', '{}'), ('s', 'sibling', '{}')`,
	} {
		if _, err := database.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := database.GetDescendantMessages(t.Context(), "root")
	if err != nil || len(messages) != 3 {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	for _, message := range messages {
		if message.SessionID == "parent" || message.SessionID == "sibling" || strings.Contains(string(message.Data), "large patch") || strings.Contains(string(message.Data), "not usage") {
			t.Fatal(message)
		}
		if message.SessionID == "root" && !strings.Contains(string(message.Data), `"tokens":{"input":10}`) {
			t.Fatal(string(message.Data))
		}
	}
	if messages, err := database.GetDescendantMessages(t.Context(), "empty"); err != nil || len(messages) != 0 {
		t.Fatalf("empty = %+v, %v", messages, err)
	}
	if _, err := database.GetDescendantMessages(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	// A parent-link cycle terminates and counts each message once.
	if _, err := database.db.Exec(`UPDATE session SET parent_id = 'deep' WHERE id = 'root'`); err != nil {
		t.Fatal(err)
	}
	if messages, err := database.GetDescendantMessages(t.Context(), "root"); err != nil || len(messages) != 3 {
		t.Fatalf("cycle = %+v, %v", messages, err)
	}
	if _, err := database.db.Exec(`DROP TABLE message`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetDescendantMessages(t.Context(), "root"); err == nil {
		t.Fatal("missing message table accepted")
	}
}
