package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// stalePlatform reports a finished turn m2 through its live lifecycle while
// its stored transcript trails it until caught up.
type stalePlatform struct {
	platforms.Platform
	mu         sync.Mutex
	current    bool
	reads      int
	readyAfter int  // reads after which the transcript is current; 0 never
	partial    bool // trailing transcript holds m2's streamed prefix
	refreshes  int
	refreshErr error
	catchUp    bool // RefreshSession makes the transcript current
}

func (p *stalePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "m2"}, nil
}

func (p *stalePlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	if p.readyAfter > 0 && p.reads > p.readyAfter {
		p.current = true
	}
	user := db.Message{ID: "m1", TimeCreated: 1, Data: json.RawMessage(`{"role":"user"}`)}
	switch {
	case p.current:
		return &platforms.SessionDetail{
			Session:  &db.Session{Status: db.StatusWaiting},
			Messages: []db.Message{user, {ID: "m2", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)}},
			Parts:    []db.Part{{MessageID: "m2", Data: json.RawMessage(`{"type":"text","text":"answer"}`)}},
		}, nil
	case p.partial:
		// A live idle edge settles the stored status before the message finishes.
		return &platforms.SessionDetail{
			Session:  &db.Session{Status: db.StatusDone},
			Messages: []db.Message{user, {ID: "m2", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant"}`)}},
			Parts:    []db.Part{{MessageID: "m2", Data: json.RawMessage(`{"type":"text","text":"answ"}`)}},
		}, nil
	}
	return &platforms.SessionDetail{Session: &db.Session{Status: db.StatusDone}, Messages: []db.Message{user}}, nil
}

type refreshingPlatform struct{ *stalePlatform }

func (p refreshingPlatform) MarkSessionDirty(string) {}

func (p refreshingPlatform) RefreshSession(context.Context, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshes++
	if p.catchUp {
		p.current = true
	}
	return p.refreshErr
}

func fastTurnRetries(t *testing.T, delays ...time.Duration) {
	t.Helper()
	previous := completedTurnRetries
	completedTurnRetries = delays
	t.Cleanup(func() { completedTurnRetries = previous })
}

func staleModule(t *testing.T, adapter platforms.Platform) (*Module, *state.DB) {
	t.Helper()
	store := openStore(t)
	m := NewModule(Deps{Store: store, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return adapter, true }})
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	t.Cleanup(func() { cancel(); m.workers.Close() })
	return m, store
}

func linkConversation(t *testing.T, store *state.DB) state.PluginConversationKey {
	t.Helper()
	key := state.PluginConversationKey{PluginID: "plugin", AccountID: "account", ThreadID: "thread"}
	if _, _, err := store.LinkPluginConversation(t.Context(), key, state.PluginConversationSession{PlatformID: "platform", SessionID: "ses"}); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSessionCompletionRefreshesStaleHistory(t *testing.T) {
	for _, tc := range []struct {
		name       string
		partial    bool
		refreshErr error
	}{
		{name: "turn missing"},
		{name: "streamed prefix", partial: true},
		{name: "refresh error", refreshErr: errors.New("refresh failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := refreshingPlatform{&stalePlatform{catchUp: true, partial: tc.partial, refreshErr: tc.refreshErr}}
			m, _ := staleModule(t, p)
			if err := m.plugins.LinkPluginSession(t.Context(), "platform", "ses", "plugin"); err != nil {
				t.Fatal(err)
			}
			m.ReplyToSession(t.Context(), "platform", "ses")
			rows, err := m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
			if err != nil || len(rows) != 1 || rows[0].Text != "answer" {
				t.Fatalf("stale history left the completion to the sweep: %+v %v", rows, err)
			}
			if p.refreshes != 1 {
				t.Fatalf("refreshes = %d, want 1", p.refreshes)
			}
		})
	}
}

func TestSessionCompletionNeverRecordsStreamedPrefix(t *testing.T) {
	fastTurnRetries(t, time.Hour)
	m, _ := staleModule(t, &stalePlatform{partial: true})
	if err := m.plugins.LinkPluginSession(t.Context(), "platform", "ses", "plugin"); err != nil {
		t.Fatal(err)
	}
	m.ReplyToSession(t.Context(), "platform", "ses")
	m.ReconcileSessionCompletions(t.Context())
	if has, err := m.plugins.HasPluginSessionCompletion(t.Context(), "plugin", "ses:m2"); err != nil || has {
		t.Fatalf("streamed prefix recorded as the completed turn: %v %v", has, err)
	}
}

func TestSessionCompletionRetriesUntilHistoryCatchesUp(t *testing.T) {
	// The idle edge's own read trails; the first retry reads the caught-up turn.
	fastTurnRetries(t, time.Millisecond, time.Hour)
	m, _ := staleModule(t, &stalePlatform{readyAfter: 1})
	if err := m.plugins.LinkPluginSession(t.Context(), "platform", "ses", "plugin"); err != nil {
		t.Fatal(err)
	}
	m.ReplyToSession(t.Context(), "platform", "ses")
	awaitRecorded(t, func() (bool, error) { return m.plugins.HasPluginSessionCompletion(t.Context(), "plugin", "ses:m2") })
}

func TestConversationReplyRefreshesStaleHistory(t *testing.T) {
	p := refreshingPlatform{&stalePlatform{catchUp: true, partial: true}}
	m, store := staleModule(t, p)
	linkConversation(t, store)
	m.ReplyToConversation(t.Context(), "platform", "ses")
	rows, err := store.ClaimPluginConversationReplies(t.Context(), 10)
	if err != nil || len(rows) != 1 || rows[0].Text != "answer" {
		t.Fatalf("stale history left the reply to the sweep: %+v %v", rows, err)
	}
}

func TestConversationReplyRetriesUntilHistoryCatchesUp(t *testing.T) {
	fastTurnRetries(t, time.Millisecond, time.Hour)
	m, store := staleModule(t, &stalePlatform{readyAfter: 1, partial: true})
	key := linkConversation(t, store)
	m.ReplyToConversation(t.Context(), "platform", "ses")
	awaitRecorded(t, func() (bool, error) { return store.HasPluginConversationReply(t.Context(), key, "ses:m2") })
}

func TestReconciliationSchedulesNoBackoff(t *testing.T) {
	fastTurnRetries(t, time.Millisecond)
	p := &stalePlatform{}
	m, store := staleModule(t, p)
	if err := m.plugins.LinkPluginSession(t.Context(), "platform", "ses", "plugin"); err != nil {
		t.Fatal(err)
	}
	linkConversation(t, store)
	m.ReconcileSessionCompletions(t.Context())
	m.reconcileConversationReplies(t.Context())
	m.workers.Close()
	if p.reads != 2 {
		t.Fatalf("reads = %d, want one per sweep with no scheduled retries", p.reads)
	}
}

func awaitRecorded(t *testing.T, has func() (bool, error)) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if ok, err := has(); err != nil || ok {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("caught-up history waits for the reconciliation sweep")
		}
	}
}

func pendingTurnRetry(m *Module, key completedTurnKey) bool {
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	return m.turnRetries[key] != nil
}

func TestCompletedTurnRetryIsBoundedAndCoalesced(t *testing.T) {
	fastTurnRetries(t, time.Millisecond, time.Millisecond)
	m, _ := staleModule(t, &stalePlatform{})
	key := completedTurnKey{"session", "platform", "ses"}
	var calls atomic.Int32
	release := make(chan struct{})
	reply := func(context.Context, string, string) bool {
		<-release
		calls.Add(1)
		return true
	}
	m.retryCompletedTurn("session", "platform", "ses", reply)
	m.retryCompletedTurn("session", "platform", "ses", reply) // rearms the pending schedule
	close(release)
	m.workers.Close()
	// The rearm lands before the first attempt finishes, so the backoff restarts
	// once: one attempt, then a full schedule of two.
	if got := calls.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if pendingTurnRetry(m, key) {
		t.Fatal("exhausted retry left its key pending")
	}
	// A stopped module schedules nothing and keeps no key.
	m.retryCompletedTurn("session", "platform", "ses", reply)
	if pendingTurnRetry(m, key) {
		t.Fatal("refused retry left its key pending")
	}
}

func TestCompletedTurnRetryRearmsAfterSuccess(t *testing.T) {
	fastTurnRetries(t, time.Millisecond, time.Hour)
	m, _ := staleModule(t, &stalePlatform{})
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	reply := func(context.Context, string, string) bool {
		entered <- struct{}{}
		if calls.Add(1) == 1 {
			<-release
		}
		return false
	}
	m.retryCompletedTurn("session", "platform", "ses", reply)
	<-entered
	m.retryCompletedTurn("session", "platform", "ses", reply) // a newer edge mid-attempt
	close(release)
	<-entered
	m.workers.Close()
	if got := calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want the newer edge retried after success", got)
	}
}

func TestCompletedTurnRetryStopsWithModuleAndRecoversPanic(t *testing.T) {
	fastTurnRetries(t, time.Millisecond, time.Hour)
	m, _ := staleModule(t, &stalePlatform{})
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	panicked := make(chan struct{})
	m.retryCompletedTurn("session", "platform", "ses", func(context.Context, string, string) bool {
		close(panicked)
		panic("boom")
	})
	<-panicked
	cancel()
	m.workers.Close()
	if pendingTurnRetry(m, completedTurnKey{"session", "platform", "ses"}) {
		t.Fatal("cancelled retry left its key pending")
	}
}

func TestReadCompletedTurnWithoutLiveLifecycleAcceptsTranscript(t *testing.T) {
	p := refreshingPlatform{&stalePlatform{catchUp: true}}
	detail, behind, err := readCompletedTurn(t.Context(), p, "ses", "")
	if err != nil || behind || detail == nil || p.refreshes != 0 {
		t.Fatalf("read = %v %v %v, refreshes %d", detail, behind, err, p.refreshes)
	}
}

func TestStoredMessageFinished(t *testing.T) {
	for data, want := range map[string]bool{
		`{"role":"assistant","finish":"stop"}`:             true,
		`{"role":"assistant","error":{"name":"APIError"}}`: true,
		`{"role":"assistant","time":{"completed":5}}`:      true,
		`{"role":"assistant","error":null}`:                false,
		`{"role":"assistant","time":{"created":1}}`:        false,
		`not json`: false,
	} {
		if got := storedMessageFinished(db.Message{Data: json.RawMessage(data)}); got != want {
			t.Errorf("%s finished = %v, want %v", data, got, want)
		}
	}
}
