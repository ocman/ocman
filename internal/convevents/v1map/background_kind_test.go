package v1map

import (
	"encoding/json"
	"testing"
)

func TestBackgroundAgentKind(t *testing.T) {
	for _, kind := range []string{"agent", "", "unknown"} {
		raw := json.RawMessage(`{"id":"child","description":"Review","kind":"` + kind + `"}`)
		ev := background(1, []json.RawMessage{raw})
		want := ""
		if kind == "agent" {
			want = kind
		}
		if len(ev.Tasks) != 1 || ev.Tasks[0].Kind != want {
			t.Fatalf("kind %q: %+v", kind, ev)
		}
	}
}
