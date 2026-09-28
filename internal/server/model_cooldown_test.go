package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// cooldownRig is a server whose "opencode" session s1 lives in a project
// listing a/x then b/y. last is the session's newest message JSON; sent
// records the model each delivered prompt carried.
type cooldownRig struct {
	srv  *Server
	mu   sync.Mutex
	busy bool
	last   string
	sent   []string
	msgs   []string
	aborts int
}

func newCooldownRig(t *testing.T) *cooldownRig {
	return newCooldownRigWith(t, state.ProjectSettings{Models: []string{"a/x", "b/y"}})
}

func newCooldownRigWith(t *testing.T, ps state.ProjectSettings) *cooldownRig {
	t.Helper()
	srv, reg := newSessionsTestServer(t)
	if err := srv.stateDB.SetProjectSettings(t.Context(), "/src/foo", ps); err != nil {
		t.Fatal(err)
	}
	r := &cooldownRig{srv: srv}
	reg.Register(&fakePlatform{
		id:       "opencode",
		sessions: []db.Session{mkSession("opencode", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.sent = append(r.sent, req.Model)
			r.msgs = append(r.msgs, req.Message)
			return nil
		},
		abortFn: func(platforms.AbortRequest) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.aborts++
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			status := db.StatusDone
			if r.busy {
				status = db.StatusBusy
			}
			d := &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/src/foo", Status: status}}
			if r.last != "" {
				d.Messages = []db.Message{{ID: "m1", Data: []byte(r.last)}}
			}
			return d, nil
		},
	})
	return r
}

// modelFor sends a model-less prompt and returns the model it went out
// with: b/y means provider a is cooled down.
func (r *cooldownRig) modelFor(t *testing.T) string {
	t.Helper()
	if err := r.srv.sessions.SendMessage(t.Context(), "opencode", platforms.SendMessageRequest{SessionID: "s1", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[len(r.sent)-1]
}

const runningAssistant = `{"role":"assistant","providerID":"a"}`

func TestRetryStatusCooldown(t *testing.T) {
	in := func(d time.Duration) int64 { return time.Now().Add(d).UnixMilli() }
	for _, tc := range []struct {
		name   string
		status autoapprove.SessionStatus
		want   string
	}{
		{"short backoff", autoapprove.SessionStatus{Type: "retry", Next: in(time.Minute)}, "a/x"},
		{"long park", autoapprove.SessionStatus{Type: "retry", Next: in(10 * time.Minute)}, "b/y"},
		{"account limit", autoapprove.SessionStatus{Type: "retry", ActionReason: "account_rate_limit", Next: in(time.Second)}, "b/y"},
		{"free tier", autoapprove.SessionStatus{Type: "retry", ActionReason: "free_tier_limit"}, "b/y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCooldownRig(t)
			r.last = runningAssistant
			r.srv.onSessionRetry("s1", tc.status)
			if got := r.modelFor(t); got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIdleErrorCooldown(t *testing.T) {
	apiErr := func(code int, headers string) string {
		return `{"role":"assistant","providerID":"a","error":{"name":"APIError","data":{"statusCode":` +
			strconv.Itoa(code) + `,"responseHeaders":` + headers + `}}}`
	}
	for _, tc := range []struct {
		name, last, want string
	}{
		{"429 with retry-after", apiErr(429, `{"retry-after":"3600"}`), "b/y"},
		{"429 without headers", apiErr(429, `{}`), "b/y"},
		{"auth status", apiErr(401, `{}`), "a/x"},
		{"provider auth error", `{"role":"assistant","providerID":"a","error":{"name":"ProviderAuthError","data":{"providerID":"a"}}}`, "a/x"},
		{"context overflow", `{"role":"assistant","providerID":"a","error":{"name":"ContextOverflowError","data":{"message":"too long"}}}`, "a/x"},
		{"clean turn", `{"role":"assistant","providerID":"a","finish":"stop"}`, "a/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCooldownRig(t)
			r.last = tc.last
			r.srv.onSessionIdle("opencode", "s1")
			if err := r.srv.queueFlushWorker().Drain(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := r.modelFor(t); got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

// A message held for the next turn must drain to the fallback provider:
// the cooldown is recorded on the idle edge before the flush runs.
func TestQueuedMessageSkipsProviderThatJustHitQuota(t *testing.T) {
	r := newCooldownRig(t)
	r.busy, r.last = true, runningAssistant
	rr := httptest.NewRecorder()
	r.srv.handleSessionMessage(rr, httptest.NewRequest(http.MethodPost, "/api/session/s1/message?platform=opencode",
		strings.NewReader(`{"message":"held","queue":true}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("queue post = %d; body=%s", rr.Code, rr.Body)
	}
	r.mu.Lock()
	r.busy = false
	r.last = `{"role":"assistant","providerID":"a","error":{"name":"APIError","data":{"statusCode":429,"responseHeaders":{}}}}`
	r.mu.Unlock()
	r.srv.onSessionIdle("opencode", "s1")
	if err := r.srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	if len(r.msgs) != 1 || r.msgs[0] != continuationPrompt || r.sent[0] != "b/y" {
		t.Fatalf("first edge sent %v %q, want only the continuation on b/y", r.sent, r.msgs)
	}
	// The continuation's turn ends cleanly; its idle edge drains the
	// held message, still past the cooled provider.
	r.last = `{"role":"assistant","providerID":"b","finish":"stop"}`
	r.mu.Unlock()
	r.srv.onSessionIdle("opencode", "s1")
	if err := r.srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) != 2 || r.msgs[1] != "held" || r.sent[1] != "b/y" {
		t.Fatalf("second edge sent %v %q, want held on b/y", r.sent, r.msgs)
	}
}

func TestQuotaResetIn(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    time.Duration
	}{
		{"none", nil, 0},
		{"retry-after seconds", map[string]string{"Retry-After": "120"}, 2 * time.Minute},
		{"retry-after-ms", map[string]string{"retry-after-ms": "1500"}, 1500 * time.Millisecond},
		{"retry-after date", map[string]string{"retry-after": now.Add(time.Hour).Format(http.TimeFormat)}, time.Hour},
		{"anthropic resets", map[string]string{
			"anthropic-ratelimit-requests-reset": now.Add(time.Minute).Format(time.RFC3339),
			"anthropic-ratelimit-tokens-reset":   now.Add(30 * time.Minute).Format(time.RFC3339),
		}, 30 * time.Minute},
		{"garbage", map[string]string{"retry-after": "soon"}, 0},
	} {
		if got := quotaResetIn(tc.headers, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
