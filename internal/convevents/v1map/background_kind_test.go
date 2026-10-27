package v1map

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convevents"
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

func TestBackgroundLifecycleDeclaration(t *testing.T) {
	for _, lifecycle := range []string{"service", "task", "", "unknown"} {
		raw := json.RawMessage(`{"id":"shell","description":"watch","lifecycle":"` + lifecycle + `"}`)
		ev := background(1, []json.RawMessage{raw})
		want := lifecycle
		if lifecycle == "unknown" {
			want = ""
		}
		if len(ev.Tasks) != 1 || ev.Tasks[0].Lifecycle != want {
			t.Fatalf("lifecycle %q: %+v", lifecycle, ev)
		}
	}
}

func TestBackgroundEventsStayInTheirSession(t *testing.T) {
	// Directory-wide streams feed all three subscriptions. Even a known child
	// must not overwrite its parent's aggregate background-work snapshot.
	for _, page := range []string{"unrelated", "parent", "child"} {
		m := newTestMapper(Options{SessionID: page, Descendant: func(id string) bool { return id == "child" }})
		for _, owner := range []string{"unrelated", "parent", "child"} {
			for _, count := range []int{1, 0} {
				payload := fmt.Sprintf(`{"directory":"/repo","payload":{"type":"ocman.session.background","properties":{"sessionID":%q,"count":%d,"tasks":[{"id":"nested","description":"Review","kind":"agent"}]}}}`, owner, count)
				events := m.Map(frame(payload)).Events
				if owner != page {
					if len(events) != 0 {
						t.Errorf("%s received %s's background update: %+v", page, owner, events)
					}
					continue
				}
				if len(events) != 1 {
					t.Fatalf("%s lost its own background update: %+v", page, events)
				}
				if got := events[0].(convevents.BackgroundChanged); got.Count != count {
					t.Errorf("%s count = %d, want %d", page, got.Count, count)
				}
			}
		}
	}
}
