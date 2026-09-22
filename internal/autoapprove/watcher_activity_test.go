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
		TimeUpdated int64  `json:"timeUpdated"`
	}
	if err := json.Unmarshal(payloads[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "background" || payload.TimeUpdated < before {
		t.Fatalf("bad activity payload: %+v", payload)
	}
	if len(rec.sortedIDs()) != 1 || rec.fullMarks() != 1 {
		t.Fatal("activity broadcast must retain cache invalidation")
	}
}
