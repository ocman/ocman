package v1map

import "testing"

func TestChildIdlePreservesTerminalFailure(t *testing.T) {
	for _, status := range []string{"error", "interrupted"} {
		m := newTestMapper(Options{})
		m.Map(frame(`{"type":"session.created","properties":{"info":{"id":"child","parentID":"s"}}}`))
		m.Map(frame(`{"type":"session.status","properties":{"sessionID":"child","status":{"type":"` + status + `"}}}`))
		if result := m.Map(frame(`{"type":"session.idle","properties":{"sessionID":"child"}}`)); len(result.Events) != 0 {
			t.Fatalf("child idle overwrote %s: %+v", status, result.Events)
		}
		m.Map(frame(`{"type":"session.status","properties":{"sessionID":"child","status":{"type":"busy"}}}`))
		if result := m.Map(frame(`{"type":"session.idle","properties":{"sessionID":"child"}}`)); len(result.Events) != 1 {
			t.Fatalf("terminal status stuck across new child work: %+v", result.Events)
		}
	}
}
