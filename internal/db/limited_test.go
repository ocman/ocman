package db

import "testing"

func TestSettleLimitedTurn(t *testing.T) {
	for _, live := range []bool{false, true} {
		for _, inferred := range []SessionStatus{StatusBusy, StatusWaiting, StatusDone, StatusError, StatusInterrupted} {
			if got := SettleSessionStatus(TurnLimited, live, inferred); got != StatusLimited {
				t.Fatalf("limited turn settled as %s", got)
			}
		}
	}
}
