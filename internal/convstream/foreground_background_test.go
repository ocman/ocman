package convstream

import (
	"testing"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
)

func TestForegroundIdlePreservesBackgroundTasks(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	sub := subscribe(t, h, bareSource{src}, "")
	sink := nextSink(t, src)
	tasks := []convevents.BackgroundTask{{ID: "vite", Description: "pnpm dev"}}
	sink.Emit(convevents.BackgroundChanged{Count: 1, Tasks: tasks}, Meta{})
	drain(sub)
	sink.Emit(idle(), Meta{})
	for _, e := range decodeAll(t, drain(sub)) {
		if _, ok := e.Event.(convevents.BackgroundChanged); ok {
			t.Fatal("foreground idle cleared the independent task ledger")
		}
	}
	sink.Emit(toolPart("vite", "running"), Meta{})
	sink.Emit(delta("vite", 0, "ready"), Meta{})
	for _, e := range decodeAll(t, drain(sub)) {
		if st, ok := e.Event.(convevents.SessionStatusEvent); ok && st.Status == db.StatusBusy {
			t.Fatal("background tool output overrode foreground idle")
		}
	}
}
