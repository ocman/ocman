package notifications

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	webpush "github.com/SherClockHolmes/webpush-go"
)

func fixture(t *testing.T) *Module {
	t.Helper()
	db, err := state.Open(statetest.Path(t, Schema), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(Deps{Store: db, Snapshot: func(context.Context) ([]Candidate, error) { return nil, nil }})
}

func subscription(t *testing.T) webpush.Subscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return webpush.Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/device", Keys: webpush.Keys{
		P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
	}}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSubscriptionValidation(t *testing.T) {
	for _, raw := range []string{"https://fcm.googleapis.com/fcm/send/x", "https://updates.push.services.mozilla.com/wpush/v2/x", "https://web.push.apple.com/x", "https://wns.notify.windows.com/x"} {
		if !validEndpoint(raw) {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"http://fcm.googleapis.com/x", "https://127.0.0.1/x", "https://fcm.googleapis.com.evil.test/x", "https://user@fcm.googleapis.com/x", "https://fcm.googleapis.com:444/x", "https://fcm.googleapis.com/x#fragment", "not a URL"} {
		if validEndpoint(raw) {
			t.Errorf("accepted %s", raw)
		}
	}
	sub := subscription(t)
	if !validSubscription(sub) {
		t.Fatal("valid subscription rejected")
	}
	sub.Keys.Auth = "invalid"
	if validSubscription(sub) {
		t.Fatal("accepted bad auth")
	}
	sub = subscription(t)
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	if validSubscription(sub) {
		t.Fatal("accepted invalid curve point")
	}
}

func TestRoutesAndKeyPersistence(t *testing.T) {
	m := fixture(t)
	sub := subscription(t)
	data, _ := json.Marshal(sub)
	for _, tc := range []struct {
		origin, body string
		status       int
	}{
		{"https://evil.test", string(data), 403}, {"", string(data), 403}, {"https://ocman.test", "{", 400},
		{"https://ocman.test", `{"endpoint":"https://127.0.0.1"}`, 400}, {"https://ocman.test", string(data), 204},
	} {
		r := httptest.NewRequest(http.MethodPost, "https://ocman.test/api/notifications/subscribe", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		m.subscribe(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
		}
	}
	firstPrivate, firstPublic, err := m.store.keys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other := New(m.d)
	private, public, err := other.store.keys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if private != firstPrivate || public != firstPublic {
		t.Fatal("keys changed across module restart")
	}
	w := httptest.NewRecorder()
	m.publicKey(w, httptest.NewRequest(http.MethodGet, "https://ocman.test/api/notifications/push-key", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), private) || !strings.Contains(w.Body.String(), public) {
		t.Fatal("key endpoint leaked secret or omitted public key")
	}
	r := httptest.NewRequest(http.MethodPost, "https://ocman.test/api/notifications/unsubscribe", strings.NewReader(`{"endpoint":"`+sub.Endpoint+`"}`))
	r.Header.Set("Origin", "https://ocman.test")
	w = httptest.NewRecorder()
	m.unsubscribe(w, r)
	if w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{`, `{"endpoint":"https://example.com/push"}`} {
		r := httptest.NewRequest(http.MethodPost, "https://ocman.test/api/notifications/unsubscribe", strings.NewReader(body))
		r.Header.Set("Origin", "https://ocman.test")
		w = httptest.NewRecorder()
		m.unsubscribe(w, r)
		if w.Code != 400 {
			t.Fatalf("unsubscribe %s: %d", body, w.Code)
		}
	}
	r = httptest.NewRequest(http.MethodPost, "https://ocman.test/api/notifications/unsubscribe", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	m.unsubscribe(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin unsubscribe: %d", w.Code)
	}
	n, err := m.store.count(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	w = httptest.NewRecorder()
	New(Deps{}).publicKey(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestBaselineDedupAndRetryAcrossRestart(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	sub := subscription(t)
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Key: "platform:session:request", Notification: Notification{Title: "ocman", URL: "/session/s", Kind: "prompt"}}
	var snapshot []Candidate
	m.d.Snapshot = func(context.Context) ([]Candidate, error) { return snapshot, nil }
	calls := 0
	m.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != sub.Endpoint || r.Header.Get("Authorization") == "" || r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Fatal("invalid Web Push request")
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Retry-After": []string{"60"}}}, nil
	})
	if err := m.tick(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot = []Candidate{candidate}
	if err := m.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if err := m.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("retry ignored backoff")
	}
	if _, err := m.store.db.Exec(`UPDATE push_delivery SET next_at=0`); err != nil {
		t.Fatal(err)
	}
	restarted := New(m.d)
	restarted.client.Transport = transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := restarted.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("pending delivery did not survive restart")
	}
	if err := restarted.store.enqueue(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err := restarted.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("duplicate receipt delivered again")
	}
}

func TestHistoricalBaselineAndExpiredSubscription(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	sub := subscription(t)
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Key: "historical", Notification: Notification{Title: "old"}}
	m.d.Snapshot = func(context.Context) ([]Candidate, error) { return []Candidate{c}, nil }
	calls := 0
	m.client.Transport = transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusGone, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := m.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("replayed historical session")
	}
	if err := m.store.enqueue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := m.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := m.store.count(ctx)
	if err != nil || n != 0 {
		t.Fatal("expired device retained")
	}
	var receipts int
	if err := m.store.db.QueryRow(`SELECT count(*) FROM push_delivery`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("unsubscribe did not cascade")
	}
}

func TestFailuresAreBoundedAndCancelRun(t *testing.T) {
	m := fixture(t)
	sub := subscription(t)
	ctx := t.Context()
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Key: "error", Notification: Notification{Title: "alert"}}
	if err := m.store.enqueue(ctx, c); err != nil {
		t.Fatal(err)
	}
	m.client.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("network") })
	if err := m.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.db.Exec(`UPDATE push_delivery SET next_at=0,attempts=5`); err != nil {
		t.Fatal(err)
	}
	if err := m.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := m.store.due(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("retry budget unbounded")
	}
	if err := m.store.enqueue(ctx, Candidate{Key: "stale"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.db.Exec(`UPDATE push_delivery SET created_at=0`); err != nil {
		t.Fatal(err)
	}
	rows, err = m.store.due(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("stale alerts not cleaned")
	}
	m.d.Snapshot = func(context.Context) ([]Candidate, error) { return nil, errors.New("offline") }
	if err := m.tick(ctx); err == nil {
		t.Fatal("snapshot error hidden")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	done := make(chan struct{})
	go func() { m.Run(cancelCtx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run ignored cancellation")
	}
	New(Deps{}).Run(ctx)
}

func TestDeviceLimitAllowsExistingDeviceRefresh(t *testing.T) {
	m := fixture(t)
	sub := subscription(t)
	for i := 0; i < 64; i++ {
		sub.Endpoint = "https://fcm.googleapis.com/" + string(rune('A'+i))
		if err := m.store.save(t.Context(), sub); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.store.save(t.Context(), sub); err != nil {
		t.Fatal("existing device could not refresh")
	}
	sub.Endpoint = "https://fcm.googleapis.com/overflow"
	if err := m.store.save(t.Context(), sub); err == nil {
		t.Fatal("device limit not enforced")
	}
}

func TestModuleRoutesEnforceMethodAndAuth(t *testing.T) {
	m := fixture(t)
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: mux, Auth: auth})
	if m.Name() != "notifications" {
		t.Fatal(m.Name())
	}
	for _, tc := range []struct {
		method, path string
		auth         bool
		status       int
	}{
		{http.MethodGet, "/api/notifications/push-key", false, http.StatusUnauthorized},
		{http.MethodGet, "/api/notifications/push-key", true, http.StatusOK},
		{http.MethodGet, "/api/notifications/subscribe", true, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/settings/notification-badge", false, http.StatusUnauthorized},
		{http.MethodPost, "/api/settings/notification-badge", false, http.StatusUnauthorized},
		{http.MethodGet, "/api/settings/notification-badge", true, http.StatusOK},
		{http.MethodDelete, "/api/settings/notification-badge", true, http.StatusMethodNotAllowed},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.auth {
			r.Header.Set("Authorization", "test")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}

func TestStoreAndRoutesSurfaceDatabaseFailures(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	sub := subscription(t)
	if err := m.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.store.keys(ctx); err == nil {
		t.Fatal("keys hid closed database")
	}
	for _, err := range []error{m.store.save(ctx, sub), m.store.remove(ctx, sub.Endpoint), m.store.enqueue(ctx, Candidate{}), m.store.finish(ctx, delivery{}, true, 0)} {
		if err == nil {
			t.Fatal("store hid closed database")
		}
	}
	if _, err := m.store.count(ctx); err == nil {
		t.Fatal("count hid closed database")
	}
	if _, err := m.store.due(ctx); err == nil {
		t.Fatal("due hid closed database")
	}
	if err := m.deliver(ctx); err == nil {
		t.Fatal("delivery hid closed database")
	}
	if err := m.tick(ctx); err == nil {
		t.Fatal("tick hid closed database")
	}
	data, _ := json.Marshal(sub)
	for _, h := range []http.HandlerFunc{m.publicKey, m.subscribe, m.unsubscribe} {
		r := httptest.NewRequest(http.MethodPost, "https://ocman.test/", strings.NewReader(string(data)))
		r.Header.Set("Origin", "https://ocman.test")
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("hidden handler failure: %d", w.Code)
		}
	}
}

func TestInvalidPersistedSubscriptionsAndCancellation(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	sub := subscription(t)
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := m.store.enqueue(ctx, Candidate{Key: "alert"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.db.Exec(`UPDATE push_subscription SET subscription='{'`); err != nil {
		t.Fatal(err)
	}
	if err := m.deliver(ctx); err == nil {
		t.Fatal("corrupt subscription ignored")
	}
	sub.Endpoint = "https://127.0.0.1/private"
	data, _ := json.Marshal(sub)
	if _, err := m.store.db.Exec(`UPDATE push_subscription SET subscription=?`, string(data)); err != nil {
		t.Fatal(err)
	}
	if err := m.deliver(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := m.store.count(ctx)
	if err != nil || n != 0 {
		t.Fatal("unsafe persisted URL retained")
	}
	if err := m.tick(ctx); err != nil || m.baseline != nil {
		t.Fatal("empty installation retains baseline")
	}
	sub = subscription(t)
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := m.store.enqueue(ctx, Candidate{Key: "cancel"}); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	m.client.Transport = transport(func(*http.Request) (*http.Response, error) { cancel(); return nil, context.Canceled })
	if err := m.deliver(cancelCtx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden")
	}
	due, err := m.store.due(ctx)
	if err != nil || len(due) != 1 || due[0].attempts != 0 {
		t.Fatal("cancellation spent retry budget")
	}
}
