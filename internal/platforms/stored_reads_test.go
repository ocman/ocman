package platforms

import (
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestStoredInferenceNeverCreatesRunningAuthority(t *testing.T) {
	disconnected := db.Session{Status: db.StatusBusy}
	OverlaySessionObservation(&disconnected, &LiveState{Status: "busy"}, false)
	if disconnected.Status != db.StatusInterrupted {
		t.Fatal("disconnected cache created running authority")
	}
	for _, tc := range []struct {
		live *LiveState
		want db.SessionStatus
	}{
		{nil, db.StatusInterrupted}, {&LiveState{Status: "busy"}, db.StatusBusy}, {&LiveState{Status: "busy", PendingPermission: true}, db.StatusWaiting}, {&LiveState{Status: "busy", PendingQuestion: true}, db.StatusWaiting},
		{&LiveState{Status: "waiting"}, db.StatusWaiting}, {&LiveState{Status: "done"}, db.StatusDone}, {&LiveState{Status: "error"}, db.StatusError}, {&LiveState{Status: "interrupted"}, db.StatusInterrupted}, {&LiveState{Status: "limited"}, db.StatusLimited}, {&LiveState{Status: "unknown"}, db.StatusInterrupted},
	} {
		row := db.Session{Status: db.StatusBusy}
		OverlaySessionObservation(&row, tc.live, tc.live != nil)
		if row.Status != tc.want {
			t.Fatalf("%+v => %s want %s", tc.live, row.Status, tc.want)
		}
	}
}

func TestPromptBubblingUsesOnlyStoredParentEvidence(t *testing.T) {
	got := BubblePromptSessions(map[string]bool{"child": true, "standalone": true, "cycle": true, "ignored": false}, map[string]string{"child": "nested", "nested": "root", "cycle": "other", "other": "cycle"})
	if !reflect.DeepEqual(got, map[string]bool{"root": true, "standalone": true, "cycle": true}) {
		t.Fatal(got)
	}
}
