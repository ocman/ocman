package db

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestDescendantMessagesUsesSessionIndex(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) { testDescendantMessagesUsesSessionIndex(t, version) })
	}
}

func testDescendantMessagesUsesSessionIndex(t *testing.T, version string) {
	database := openTestDB(t)
	defer database.Close()
	queries := []string{
		`CREATE INDEX message_session_id_idx ON message(session_id)`,
		`CREATE INDEX session_parent_id_idx ON session(parent_id)`,
	}
	if version == "v2" {
		queries = append([]string{v2SchemaDDL}, v2ViewDDL...)
		queries = append(queries,
			`CREATE INDEX message_session_id_idx ON session_message(session_id)`,
			`CREATE INDEX session_parent_id_idx ON session_v2(parent_id)`)
	}
	for _, query := range queries {
		if _, err := database.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := database.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+descendantMessagesQuery, "root")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var indexed bool
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.Contains(detail, "SCAN m") {
			t.Error("usage scans unrelated messages instead of looking up descendant sessions")
		}
		indexed = indexed || strings.Contains(detail, "SEARCH m USING INDEX message_session_id_idx")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Error("usage did not use the message session index")
	}
}

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
