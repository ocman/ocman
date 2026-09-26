package opencode

import (
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestSessionStatusOnPortBoundedHistory(t *testing.T) {
	messages := make([]string, 2001)
	for i := range messages {
		messages[i] = `{"role":"assistant","finish":"stop","padding":"` + strings.Repeat("x", 4096) + `"}`
	}
	messages[len(messages)-1] = `{"role":"assistant","error":{"name":"APIError"}}`
	database := newTestDBWithSessions(t, []testSession{{id: "ses-1", directory: "/repo", messages: messages}})
	a := New(database, nil)
	a.ObserveSessionStatus("7777", 0, "ses-1", "idle")
	allocs := testing.AllocsPerRun(3, func() {
		status, err := a.SessionStatusOnPort("ses-1", "7777")
		if err != nil || status != db.StatusError {
			t.Fatalf("status = %q, err = %v, want error status", status, err)
		}
	})
	t.Logf("status read allocated %.0f objects for %d messages", allocs, len(messages))
	// A latest-message read needs tens of allocations, independent of history.
	if allocs > 200 {
		t.Fatalf("status read allocated %.0f objects for 2001 messages; want <= 200", allocs)
	}
}
