package opencode

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestObserveSessionStatusCoalescesIdlePair(t *testing.T) {
	a := New(nil, nil)
	for _, step := range []struct {
		status  string
		changed bool
	}{
		{"busy", true}, {"idle", true}, {"idle", false},
		{"busy", true}, {"retry", false}, {"idle", true},
	} {
		if changed := a.ObserveSessionStatus("7777", 0, "s", step.status); changed != step.changed {
			t.Fatalf("%s changed=%v, want %v", step.status, changed, step.changed)
		}
	}
	if !a.ObserveSessionStatus("8888", 0, "s", "idle") {
		t.Fatal("new instance must emit status")
	}
}

func TestSessionStatusOnPortDoesNotReadHistoricalMessages(t *testing.T) {
	// Invalid historical JSON makes any attempt to parse the transcript fail.
	// Only the newest message is relevant to the terminal status.
	messages := make([]string, 1000)
	for i := range messages {
		messages[i] = "not json"
	}
	messages = append(messages, `{"role":"assistant","finish":"stop"}`)
	database := newTestDBWithSessions(t, []testSession{{id: "s", directory: "/repo", messages: messages}})
	a := New(database, nil)
	a.ObserveSessionStatus("7777", 0, "s", "idle")
	status, err := a.SessionStatusOnPort("s", "7777")
	if err != nil || status != db.StatusWaiting {
		t.Fatalf("status=%q err=%v, want waiting without reading history", status, err)
	}
}
