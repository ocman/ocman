package opencode

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestObservedBusyAgreesAcrossReadsBeforeSnapshotSeed(t *testing.T) {
	a := newTestAdapter()
	const port, id = "7777", "running-session"
	// The stream has delivered an authoritative event, but another directory's
	// bootstrap read has not completed, so the port is not fully seeded yet.
	a.ObserveSessionStatus(port, 0, id, "busy")
	listed := a.settleStatusOnPort(id, port, db.StatusBusy)
	row := db.Session{ID: id, Status: db.StatusBusy}
	observations := a.CachedSessionObservations()
	var live *platforms.LiveState
	if observation, ok := observations[id]; ok {
		live = &observation
	}
	platforms.OverlaySessionObservation(&row, live, true)
	if listed != db.StatusBusy || row.Status != listed {
		t.Fatalf("running session disagrees across reads: list=%s detail/sidebar=%s", listed, row.Status)
	}
}

func TestCachedObservationsRejectDisconnectedAndStaleGeneration(t *testing.T) {
	a := newTestAdapter()
	a.ObserveSessionStatus("7777", 0, "running-session", "busy")
	a.ClearSessionStatusForPort("7777")
	a.ClearSessionStatusForPort("7777")
	if a.ObserveSessionStatus("7777", 1, "running-session", "busy") {
		t.Fatal("retired stream restored a running observation")
	}
	a.ObserveSessionStatus("", 0, "unowned-session", "busy")
	if got := a.CachedSessionObservations(); len(got) != 0 {
		t.Fatalf("disconnected or unowned lifecycle escaped: %+v", got)
	}
}
