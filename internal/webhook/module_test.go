package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type testRouter struct{ *http.ServeMux }

func (r testRouter) Handle(p string, h http.HandlerFunc)       { r.HandleFunc(p, h) }
func (r testRouter) Local(p string, h http.HandlerFunc)        { r.HandleFunc(p, h) }
func (r testRouter) Setting(p string, h http.HandlerFunc)      { r.HandleFunc(p, h) }
func (r testRouter) LocalSetting(p string, h http.HandlerFunc) { r.HandleFunc(p, h) }

type fakeRoutines struct{ runs atomic.Int32 }

func (f *fakeRoutines) RunWebhook(context.Context, string, string, int64) (state.RoutineRun, error) {
	f.runs.Add(1)
	return state.RoutineRun{}, nil
}

func (*fakeRoutines) Get(_ context.Context, id string) (state.Routine, error) {
	if id == "missing" {
		return state.Routine{}, state.ErrRoutineNotFound
	}
	return state.Routine{ID: id}, nil
}

// relayURL is read through Deps.RelayURL, so a test can point it at a fake
// relay after the module is built, as Settings can at runtime.
var relayURL string

func testModule(t *testing.T) (*Module, http.Handler) {
	t.Helper()
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); relayURL = "" })
	m := NewModule(Deps{Store: store, Routines: &fakeRoutines{}, RelayURL: func() string { return relayURL }})
	mux := http.NewServeMux()
	m.Routes(testRouter{mux})
	return m, mux
}

func do(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func fakeInboxRelay(t *testing.T) *httptest.Server {
	t.Helper()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/inboxes":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"inbox","ingestionUrl":"/i/inbox/token","managementToken":"manage","fetchToken":"fetch","acknowledgmentToken":"ack","keyVersion":1,"secretHeader":"Authorization"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/inboxes/inbox/rotate":
			_, _ = w.Write([]byte(`{"keyVersion":2}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/inboxes/inbox":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && r.URL.Path == "/inboxes/inbox/secret":
			var body struct{ Secret, SecretHeader string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Secret != "" && body.SecretHeader == "" {
				body.SecretHeader = "X-Webhook-Secret"
			}
			if body.Secret == "" {
				body.SecretHeader = ""
			}
			_, _ = w.Write([]byte(`{"secretHeader":"` + body.SecretHeader + `"}`))
		default:
			http.Error(w, "relay failed", http.StatusBadGateway)
		}
	}))
	t.Cleanup(relay.Close)
	return relay
}

func TestWebhookInboxPatchKeepsConcurrentKeyAndRevocation(t *testing.T) {
	m, handler := testModule(t)
	relayURL = fakeInboxRelay(t).URL
	if rec := do(t, handler, http.MethodPost, "/api/webhook-inboxes", `{"name":"forgejo"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	stale, err := m.store.GetWebhookInboxByID(t.Context(), "inbox")
	if err != nil {
		t.Fatal(err)
	}
	// A key reset lands while a rename still holds the pre-reset snapshot.
	if rec := do(t, handler, http.MethodPut, "/api/webhook-inboxes/inbox", `{"reset":true}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	rotated, _ := m.store.GetWebhookInboxByID(t.Context(), "inbox")
	rec := httptest.NewRecorder()
	m.updateInbox(rec, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"renamed","secret":"s"}`)), stale)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := m.store.GetWebhookInboxByID(t.Context(), "inbox")
	if got.Identity != rotated.Identity || got.KeyVersion != rotated.KeyVersion || got.Identity == stale.Identity || got.Name != "renamed" || got.SecretHeader != "X-Webhook-Secret" {
		t.Fatalf("patch clobbered the rotated key: stale v%d, rotated v%d, got %+v", stale.KeyVersion, rotated.KeyVersion, got)
	}
	var view InboxView
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.KeyVersion != rotated.KeyVersion {
		t.Fatalf("view = %s", rec.Body.String())
	}

	// A secret replacement lands while a rename still holds the old snapshot:
	// the rename must not restore the old secret the relay no longer accepts.
	stale, _ = m.store.GetWebhookInboxByID(t.Context(), "inbox")
	if rec := do(t, handler, http.MethodPatch, "/api/webhook-inboxes/inbox", `{"secret":"Bearer new","secretHeader":"Authorization"}`); rec.Code != http.StatusOK {
		t.Fatalf("secret: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	m.updateInbox(rec, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"renamed again"}`)), stale)
	if got, _ := m.store.GetWebhookInboxByID(t.Context(), "inbox"); rec.Code != http.StatusOK || got.Name != "renamed again" || got.Secret != "Bearer new" || got.SecretHeader != "Authorization" {
		t.Fatalf("rename restored the stale secret: %d %+v", rec.Code, got)
	}

	// A rename that races a revoke must not bring the inbox back.
	if err := m.store.DeleteWebhookInboxByID(t.Context(), "inbox"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	m.updateInbox(rec, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"ghost"}`)), stale)
	if _, err := m.store.GetWebhookInboxByID(t.Context(), "inbox"); err == nil {
		t.Fatal("patch resurrected a revoked inbox")
	}
}

// Two secret changes race. The relay takes A and holds its reply until B
// arrives, then takes B; unless the relay call and the local write share a
// lock, ocman records A last while the relay enforces B. With the lock B
// cannot arrive during the hold, which then just times out.
func TestWebhookSecretChangesPersistInRelayOrder(t *testing.T) {
	m, handler := testModule(t)
	var mu sync.Mutex
	var last string
	second := make(chan struct{})
	var arrivals atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inboxes/inbox/secret" {
			http.Error(w, "unexpected", http.StatusBadGateway)
			return
		}
		var body struct{ Secret string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		last = body.Secret
		mu.Unlock()
		if arrivals.Add(1) == 1 {
			select {
			case <-second:
			case <-time.After(200 * time.Millisecond):
			}
		} else {
			defer close(second)
		}
		_, _ = w.Write([]byte(`{"secretHeader":"X-Webhook-Secret"}`))
	}))
	t.Cleanup(relay.Close)
	if err := m.store.SaveWebhookInbox(t.Context(), state.WebhookInbox{ID: "inbox", RoutineID: "inbox", RelayURL: relay.URL, Identity: "id"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, secret := range []string{"A", "B"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(t, handler, http.MethodPatch, "/api/webhook-inboxes/inbox", `{"secret":"`+secret+`"}`)
		}()
		time.Sleep(20 * time.Millisecond) // A reaches the relay first
	}
	wg.Wait()
	got, _ := m.store.GetWebhookInboxByID(t.Context(), "inbox")
	mu.Lock()
	defer mu.Unlock()
	if got.Secret != last {
		t.Fatalf("ocman stored %q but the relay enforces %q", got.Secret, last)
	}
}

// Delivery files expire even after the last inbox is revoked, when no poller
// is left running.
func TestWebhookHistoryCleanupRunsWithoutInboxes(t *testing.T) {
	m, _ := testModule(t)
	path, err := m.store.WriteWebhookFile("gone", "d1", state.WebhookBodyFile, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-state.WebhookHistoryRetention - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if inboxes, _ := m.store.ListWebhookInboxes(t.Context()); len(inboxes) != 0 {
		t.Fatalf("inboxes = %+v", inboxes)
	}
	m.cleanHistory(t.Context())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired delivery file kept: %v", err)
	}
}

// Run polls each inbox loaded by Start plus one created afterwards, and on
// shutdown waits for all of them before returning.
func TestModuleRunPollsInboxesAndWaitsOnShutdown(t *testing.T) {
	polled := make(chan string, 4)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polled <- strings.Split(strings.TrimPrefix(r.URL.Path, "/inboxes/"), "/")[0]
		_, _ = w.Write([]byte(`{"deliveries":[]}`))
	}))
	t.Cleanup(relay.Close)
	m, _ := testModule(t)
	id := mustIdentity(t)
	for _, inbox := range []string{"early", "late"} {
		if err := m.store.SaveWebhookInbox(t.Context(), state.WebhookInbox{ID: inbox, RoutineID: inbox, RelayURL: relay.URL, Identity: id}); err != nil {
			t.Fatal(err)
		}
		if inbox == "early" {
			if err := m.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
	late, _ := m.store.GetWebhookInboxByID(t.Context(), "late")
	m.startPoller(late) // before Run: dropped, the next startup polls it

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	if got := <-polled; got != "early" {
		t.Fatalf("first poll = %q, want early", got)
	}
	m.startPoller(late)
	if got := <-polled; got != "late" {
		t.Fatalf("second poll = %q, want late", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	m.startPoller(late) // after shutdown: dropped
	if len(polled) != 0 {
		t.Fatalf("unexpected extra polls: %d", len(polled))
	}
}

func TestModuleStartFailsOnBrokenStore(t *testing.T) {
	m, _ := testModule(t)
	_ = m.store.Close()
	if err := m.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded on a closed store")
	}
}

func mustIdentity(t *testing.T) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
