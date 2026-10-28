package platforms

import (
	"github.com/ocman/ocman/internal/db"
	"testing"
)

func TestReloadSessionActive(t *testing.T) {
	for _, status := range []db.SessionStatus{db.StatusBusy, db.StatusWaiting, db.StatusLimited, db.StatusDone, db.StatusError, db.StatusInterrupted} {
		want := status == db.StatusBusy || status == db.StatusLimited
		if ReloadSessionActive(db.Session{Status: status}) != want {
			t.Fatalf("status %s", status)
		}
	}
	for _, s := range []db.Session{{PendingPermission: true}, {PendingQuestion: true}} {
		if !ReloadSessionActive(s) {
			t.Fatal("pending prompt ignored")
		}
	}
}
