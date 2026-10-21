package platforms

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestIdleObservationPreservesStoredOutcome(t *testing.T) {
	for _, inferred := range []db.SessionStatus{db.StatusWaiting, db.StatusError, db.StatusDone, db.StatusBusy} {
		row := db.Session{Status: inferred}
		OverlaySessionObservation(&row, &LiveState{Status: "idle"}, true)
		want := db.SettleSessionStatus(db.TurnSettled, true, inferred)
		if row.Status != want {
			t.Fatalf("inferred=%s idle=%s want=%s", inferred, row.Status, want)
		}
	}
}
