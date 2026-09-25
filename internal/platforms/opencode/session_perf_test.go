package opencode

import (
	"fmt"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestIncrementalRefreshRecordsBudget(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)
	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	MarkSessionDirty("s-new")
	started := time.Now()
	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	sessionsMu.RLock()
	end, cost := lastRefreshEnd, lastRefreshCost
	sessionsMu.RUnlock()
	if end.Before(started) || cost <= 0 {
		t.Fatalf("incremental refresh did not record its budget: end=%v cost=%v", end, cost)
	}
}

func TestSessionsResolvesEachDirectoryOnce(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)
	const dir = "/repo/main"
	fixture := make([]testSession, 100)
	for i := range fixture {
		fixture[i] = testSession{id: fmt.Sprintf("s%d", i), directory: dir}
	}
	database := newTestDBWithSessions(t, fixture)
	withTestPort(t, dir, "7777")
	original := resolveSessionDirectory
	t.Cleanup(func() { resolveSessionDirectory = original })
	for _, connected := range []bool{true, false} {
		calls := 0
		resolveSessionDirectory = func(directory string) string {
			calls++
			if connected {
				return directory
			}
			return "/missing"
		}
		sessions, err := New(database, nil).Sessions(t.Context(), "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("connected=%v: resolved directory %d times, want 1", connected, calls)
		}
		if len(sessions) != len(fixture) {
			t.Fatalf("got %d sessions, want %d", len(sessions), len(fixture))
		}
		for _, session := range sessions {
			if session.LiveConnection != connected {
				t.Fatalf("session %s live=%v, want %v", session.ID, session.LiveConnection, connected)
			}
		}
	}
}

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
