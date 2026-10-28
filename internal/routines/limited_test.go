package routines

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestLimitedRunRemainsRunning(t *testing.T) {
	h := newHarness(t)
	routine, err := h.svc.Create(t.Context(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}
	h.platform.setStatus(db.StatusLimited)
	for _, settle := range []func() error{
		func() error { return h.svc.Tick(t.Context()) },
		func() error { return h.svc.Recover(t.Context()) },
	} {
		if err := settle(); err != nil {
			t.Fatal(err)
		}
		runs, err := h.svc.History(t.Context(), routine.ID)
		if err != nil || len(runs) != 1 || runs[0].State != RunRunning || runs[0].FinishedAt != 0 {
			t.Fatalf("runs = %+v, %v", runs, err)
		}
	}
	// A later genuine completion can settle the same run.
	h.platform.setStatus(db.StatusDone)
	if err := h.svc.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	runs, err := h.svc.History(t.Context(), routine.ID)
	if err != nil || runs[0].State != RunSuccess {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
}
