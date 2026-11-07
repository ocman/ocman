package autoapprove

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSessionDataBroadcastsActivity(t *testing.T) {
	var events []string
	var payloads [][]byte
	w, rec := newRecordingWatcher(t)
	w.svc = NewService(Deps{BroadcastGlobalEvent: func(event string, data []byte) {
		events = append(events, event)
		payloads = append(payloads, data)
	}})
	before := time.Now().UnixMilli()
	w.handleSessionDataChanged("")
	w.handleSessionDataChanged("background")
	if len(events) != 1 || events[0] != "ocman.session.activity" {
		t.Fatalf("events = %v, want one activity event", events)
	}
	var payload struct {
		SessionID   string `json:"sessionID"`
		Platform    string `json:"platform"`
		TimeUpdated int64  `json:"timeUpdated"`
	}
	if err := json.Unmarshal(payloads[0], &payload); err != nil {
		t.Fatal(err)
	}
	// Without a platform, ingestion wakes every source, including Claude Code,
	// for each streamed OpenCode part.
	if payload.SessionID != "background" || payload.Platform != "opencode" || payload.TimeUpdated < before {
		t.Fatalf("bad activity payload: %+v", payload)
	}
	if ids := rec.sortedIDs(); len(ids) != 1 || ids[0] != "background" {
		t.Fatalf("activity broadcast must still request an ingestion reread: %v", ids)
	}
}
