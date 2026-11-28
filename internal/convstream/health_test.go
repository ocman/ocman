package convstream

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
)

func TestHealthObservesOneLiveTurnAndItsAbortReceipt(t *testing.T) {
	type measurement struct {
		outcome string
		latency time.Duration
		text    bool
	}
	var got []measurement
	now := time.Now()
	h := newTestHub(t, Options{Now: func() time.Time { return now }, BeginHealthTurn: func(Key) func(string, time.Duration, bool) {
		return func(outcome string, latency time.Duration, text bool) {
			got = append(got, measurement{outcome, latency, text})
		}
	}})
	src := newFakeSource()
	subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(status(db.StatusBusy), Meta{})
	sink.Emit(status(db.StatusBusy), Meta{})
	part := textPart("text", "not sent to telemetry", false)
	sink.Emit(convevents.MessageUpserted{Message: db.Message{ID: part.Part.MessageID, Data: []byte(`{"role":"assistant"}`)}}, Meta{})
	sink.Emit(part, Meta{Replayed: true})
	now = now.Add(3 * time.Second)
	sink.Emit(part, Meta{})
	abort := h.BeginHealthCancel(testKey)
	if abort == nil {
		t.Fatal("abort didn't find the observed turn")
	}
	sink.Emit(status(db.StatusInterrupted), Meta{})
	sink.Emit(status(db.StatusBusy), Meta{})
	abort(true)
	sink.Emit(status(db.StatusError), Meta{})
	if len(got) != 2 || got[0] != (measurement{"canceled", 3 * time.Second, true}) || got[1] != (measurement{"error", 0, false}) {
		t.Fatalf("measurements = %+v", got)
	}
}
