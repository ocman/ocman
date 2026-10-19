package statev2

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"
)

// This opt-in probe only opens an existing store read-only. It never migrates
// or starts ingestion, and reports metadata/counts rather than message content.
func TestConversationReadLatency(t *testing.T) {
	path := os.Getenv("OCMAN_CONVERSATION_PERF_DB")
	if path == "" {
		t.Skip("set OCMAN_CONVERSATION_PERF_DB to an existing store for read-only timing")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)"}
	conn, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := &DB{db: conn, path: path}
	rows, err := conn.Query(`SELECT instance_id,id FROM session ORDER BY updated_at DESC LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	var keys []Key
	for rows.Next() {
		var key Key
		if err := rows.Scan(&key.InstanceID, &key.SessionID); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, key := range keys {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		start := time.Now()
		page, err := store.ReadConversation(ctx, key, 30, 0)
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			t.Errorf("%s read: %v (%s)", key.SessionID, err, elapsed)
			continue
		}
		t.Logf("session=%s duration=%s messages=%d/%d parts=%d tree=%d", key.SessionID, elapsed, len(page.Messages), page.TotalMessages, len(page.Parts), len(page.Tree))
		if elapsed > 250*time.Millisecond {
			t.Errorf("conversation read exceeded 250ms: %s", elapsed)
		}
	}
}
