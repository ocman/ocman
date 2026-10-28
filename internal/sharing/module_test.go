package sharing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/testutil"
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
	store := statetest.Open(t)
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

type blockingSessionAdapter struct {
	platforms.Platform
	entered  chan struct{}
	returned chan struct{}
}

func (blockingSessionAdapter) ID() platforms.ID { return "opencode" }

func (a blockingSessionAdapter) Session(ctx context.Context, _ string, _, _ int) (*platforms.SessionDetail, error) {
	close(a.entered)
	<-ctx.Done()
	close(a.returned)
	return nil, ctx.Err()
}

// A publish started by an idle edge runs under the module's lifetime and is
// joined by Run, so it never reads state.db after shutdown.
func TestRunJoinsIdlePublisher(t *testing.T) {
	store := statetest.Open(t)
	if _, err := store.CreateShareLink(t.Context(), "opencode", "s", 0); err != nil {
		t.Fatal(err)
	}
	adapter := blockingSessionAdapter{entered: make(chan struct{}), returned: make(chan struct{})}
	m := NewModule(Deps{Store: store, Platform: func(platforms.ID) (platforms.Platform, bool) { return adapter, true }})
	var bus module.Bus
	m.Subscribe(&bus)
	ctx, cancel := context.WithCancel(t.Context())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	go func() { m.Run(ctx); close(ran) }()
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "s"})
	<-adapter.entered
	cancel()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return: the publisher ignored the lifetime")
	}
	select {
	case <-adapter.returned:
	default:
		t.Fatal("Run returned before the publisher")
	}
}

func TestHandleSessionSharesListsActiveLinks(t *testing.T) {
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	link, err := store.CreateShareLink(t.Context(), "opencode", "s", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: store})
	adapter := blockingSessionAdapter{}
	rec := httptest.NewRecorder()
	m.handleSessionShares(rec, httptest.NewRequest(http.MethodGet, "/", nil), "s", adapter)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"token":"`+link.Token+`"`) {
		t.Fatalf("shares = %d %s", rec.Code, rec.Body)
	}
	_ = store.Close()
	rec = httptest.NewRecorder()
	m.handleSessionShares(rec, httptest.NewRequest(http.MethodGet, "/", nil), "s", adapter)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("closed store = %d", rec.Code)
	}
}
