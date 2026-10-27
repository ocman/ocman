package db

import "testing"

func TestShouldResurfaceSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  Session
		mode string
		want bool
	}{
		{"unchanged", Session{TimeUpdated: 100, Status: StatusDone}, "halt", false},
		{"busy", Session{TimeUpdated: 101, Status: StatusBusy}, "halt", false},
		{"done", Session{TimeUpdated: 101, Status: StatusDone}, "halt", true},
		{"unfinished task", Session{TimeUpdated: 101, Status: StatusDone, BackgroundTaskCount: 1}, "halt", false},
		{"only services", Session{TimeUpdated: 101, Status: StatusDone, BackgroundTaskCount: 2, BackgroundServiceCount: 2}, "halt", true},
		{"mixed", Session{TimeUpdated: 101, Status: StatusDone, BackgroundTaskCount: 2, BackgroundServiceCount: 1}, "halt", false},
		{"permission", Session{TimeUpdated: 101, Status: StatusBusy, BackgroundTaskCount: 2, PendingPermission: true}, "halt", true},
		{"question", Session{TimeUpdated: 101, Status: StatusWaiting, BackgroundTaskCount: 2, PendingQuestion: true}, "halt", true},
		{"activity", Session{TimeUpdated: 101, Status: StatusBusy, BackgroundTaskCount: 2}, "activity", true},
		{"old activity", Session{TimeUpdated: 100, Status: StatusBusy}, "activity", false},
		{"error with background", Session{TimeUpdated: 101, Status: StatusError, BackgroundTaskCount: 1}, "halt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldResurfaceSession(tc.row, 100, tc.mode); got != tc.want {
				t.Fatalf("resurface=%v want %v", got, tc.want)
			}
		})
	}
}
