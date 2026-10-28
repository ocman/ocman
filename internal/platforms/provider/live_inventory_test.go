package provider

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
)

// On reconnect, ingestion discovers sessions from the supervisor's status
// inventory before any event or creation response arrives. Runtime snapshot
// fallback must still be able to route the ID to that supervisor.
func TestLiveInventorySeedsSessionOwnership(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.reply("GET", "/sessions", []db.Session{{ID: "runtime-session", Status: db.StatusBusy}})
	a := f.adapter(t)
	rows, err := a.LiveSessions(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("live inventory: %+v %v", rows, err)
	}
	if !a.Knows("runtime-session") {
		t.Fatal("live inventory did not seed owner routing; transcript fallback cannot find its supervisor")
	}
}
