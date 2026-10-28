package db

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

// storedFixture imports a native test inventory before testing analytics. The
// assertions exercise the same projection queries production uses, not a second
// implementation of analytics over native message JSON.
func storedFixture(t *testing.T, native *DB) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "analytics.db")
	store, err := statev2.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "fixture", OwnerID: "owner", Platform: "test", SourceKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
	entries, err := native.HistoryInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		snapshot, err := native.HistorySnapshot(t.Context(), entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		s := snapshot.Session
		batch := statev2.Batch{Complete: true, Session: statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: s.ID},
			Title: s.Title, Directory: s.Directory, ParentID: s.ParentID, CreatedAt: s.TimeCreated, UpdatedAt: s.TimeUpdated, Subagent: snapshot.Subagent}}
		for i, row := range snapshot.Messages {
			var md MessageData
			if err := json.Unmarshal(row.Data, &md); err != nil {
				t.Fatal(err)
			}
			message := statev2.Message{ID: row.ID, Position: int64(i), Role: md.Role, CreatedAt: row.TimeCreated, Extensions: row.Data}
			batch.Messages = append(batch.Messages, message)
		}
		positions := map[string]int64{}
		for _, row := range snapshot.Parts {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(row.Data, &fields); err != nil {
				t.Fatal(err)
			}
			var kind, text string
			_ = json.Unmarshal(fields["type"], &kind)
			_ = json.Unmarshal(fields["text"], &text)
			delete(fields, "type")
			delete(fields, "text")
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			batch.Parts = append(batch.Parts, statev2.Part{ID: row.ID, MessageID: row.MessageID, Position: positions[row.MessageID],
				Kind: kind, Text: text, TextPresent: text != "", CreatedAt: row.TimeCreated, Extensions: raw})
			positions[row.MessageID]++
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenHistoryAnalytics(path, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}
