package statev2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyticsProjectionIsCompactAtomicAndRetainsUnknownTiming(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Complete = true
	b.Messages[0].Extensions = json.RawMessage(`{"metadata":{"role":"assistant","providerID":"p","modelID":"m","tokens":{"output":10},"time":{"created":10,"completed":100},"finish":"stop"},"native":{"huge":"discard from projection"}}`)
	b.Parts = []Part{{ID: "tool", MessageID: "message", Kind: "tool", Extensions: json.RawMessage(`{"tool":"bash","state":{"input":{"secret":"not needed"},"output":"huge result","time":{"start":20}}}`)}}
	commit(t, d, b)
	var raw string
	if err := d.db.QueryRow(`SELECT data FROM analytics_message`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "native") || strings.Contains(raw, "huge") || !strings.Contains(raw, `"output":10`) {
		t.Fatalf("message projection: %s", raw)
	}
	if err := d.db.QueryRow(`SELECT data FROM analytics_part`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "huge") || strings.Contains(raw, "secret") || !strings.Contains(raw, `"start":20`) || strings.Contains(raw, `"end"`) {
		t.Fatalf("tool projection: %s", raw)
	}
	// Malformed timing metadata fails the entire ingestion, not only projection.
	bad := b
	bad.ExpectedRevision = 1
	bad.Checkpoint = &CheckpointUpdate{Stream: "runtime/generation", ExpectedCursor: "1", Cursor: "2"}
	bad.Parts = []Part{{ID: "bad", MessageID: "message", Kind: "tool", Extensions: json.RawMessage(`{"state":"wrong shape"}`)}}
	if _, err := d.Commit(t.Context(), bad); err == nil {
		t.Fatal("committed an invalid projection")
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatalf("projection failure advanced checkpoint: %s %v", cursor, err)
	}
	if err := d.db.QueryRow(`SELECT data FROM analytics_part`).Scan(&raw); err != nil || !strings.Contains(raw, `"start":20`) {
		t.Fatal("projection partially changed", err)
	}
	// A later complete snapshot removes tool timing that is no longer present.
	b.ExpectedRevision = 1
	b.Checkpoint = bad.Checkpoint
	b.Parts = nil
	commit(t, d, b)
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM analytics_part`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retained removed timing: %d %v", count, err)
	}
}
