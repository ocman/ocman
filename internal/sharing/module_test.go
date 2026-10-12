package sharing

import (
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

type idleAdapter struct {
	platforms.Platform
	looked chan string
}

type panickingAdapter struct{ platforms.Platform }

func (panickingAdapter) ID() platforms.ID { panic("publisher panic") }

func TestIdlePublisherRecoversPanic(t *testing.T) {
	oldHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(oldHooks) })
	hook := logtest.NewGlobal()
	m := NewModule(Deps{Platform: func(platforms.ID) (platforms.Platform, bool) {
		return panickingAdapter{}, true
	}})
	var bus module.Bus
	m.Subscribe(&bus)
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "s"})
	testutil.WaitFor(t, time.Second, "publisher panic logged", func() bool {
		return hook.LastEntry() != nil
	})
	entry := hook.LastEntry()
	if entry.Level != log.ErrorLevel || entry.Data["panic"] != "publisher panic" || entry.Data["stack"] == "" || entry.Data["stack"] == nil {
		t.Fatalf("panic log = %+v", entry)
	}
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
