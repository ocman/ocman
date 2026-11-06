package convstream

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
)

func TestPumpDrainsOldEpochBeforeSourceReset(t *testing.T) {
	for range 20 {
		h := newTestHub(t, Options{})
		src := newFakeSource()
		src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
		sub := subscribe(t, h, src, "")
		nextSink(t, src)
		// Let the source fail and reconnect while the HTTP consumer is paused.
		src.end <- errBoom
		nextSink(t, src)
		<-sub.Done()
		out := httptest.NewRecorder()
		pump := &sseOut{w: out, rc: http.NewResponseController(out)}
		reason, ok := pump.pump(t.Context(), sub, nil)
		if !ok || reason != convevents.ResetSourceRestarted {
			t.Fatalf("pump = %q, %v", reason, ok)
		}
		client := &sseClient{r: bufio.NewReader(strings.NewReader(out.Body.String()))}
		client.nextOCE(t)
		if _, event := client.nextOCE(t); event.Event.EventType() != convevents.TypeStreamError {
			t.Fatalf("got %s before reset, want stream.error", event.Event.EventType())
		}
		if _, event := client.nextOCE(t); event.Event.EventType() != convevents.TypeStreamReset {
			t.Fatalf("got %s, want stream.reset", event.Event.EventType())
		}
	}
}
