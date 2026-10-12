package sharing

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type idleAdapter struct {
	platforms.Platform
	looked chan string
}

func (a idleAdapter) ID() platforms.ID { a.looked <- "opencode"; return "opencode" }

// The idle edge publishes through the adapter that owns the session, and
// an edge from an unknown platform is ignored.
func TestSubscribePublishesOnSessionIdle(t *testing.T) {
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	adapter := idleAdapter{looked: make(chan string, 1)}
	m := NewModule(Deps{Store: store, Platform: func(id platforms.ID) (platforms.Platform, bool) {
		return adapter, id == "opencode"
	}})
	var bus module.Bus
	m.Subscribe(&bus)
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "elsewhere", SessionID: "s"})
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "s"})
	select {
	case <-adapter.looked:
	case <-time.After(5 * time.Second):
		t.Fatal("idle edge did not reach the share publisher")
	}
}
