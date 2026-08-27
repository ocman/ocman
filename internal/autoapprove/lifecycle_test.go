package autoapprove

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type lifecycleStore struct {
	mu              sync.Mutex
	enabled         bool
	exists          bool
	delay           int64
	updates         []state.PermissionLifecycle
	upsertErr       error
	attempted       chan struct{}
	getEnabledBlock <-chan struct{}
}

func (s *lifecycleStore) GetAutoApprove(context.Context, string, string) (bool, bool, error) {
	if s.getEnabledBlock != nil {
		<-s.getEnabledBlock
	}
	return s.enabled, s.exists, nil
}
func (s *lifecycleStore) GetJudgeDelayMs(context.Context) (int64, error) { return s.delay, nil }
func (*lifecycleStore) GetPromptSections(context.Context) ([]state.PromptSection, error) {
	return nil, nil
}
func (*lifecycleStore) GetSetting(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (*lifecycleStore) RecordApprovedPermission(context.Context, string, string, state.ApprovedPermission) error {
	return nil
}
func (s *lifecycleStore) UpsertPermissionLifecycle(_ context.Context, update state.PermissionLifecycle) error {
	if s.attempted != nil {
		select {
		case s.attempted <- struct{}{}:
		default:
		}
	}
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.mu.Lock()
	s.updates = append(s.updates, update)
	s.mu.Unlock()
	return nil
}

func TestServiceLifecyclePersistenceFailureDoesNotBlockCachedApproval(t *testing.T) {
	store := &lifecycleStore{
		enabled:   true,
		exists:    true,
		upsertErr: errors.New("state unavailable"),
		attempted: make(chan struct{}, 1),
	}
	replied := make(chan struct{}, 1)
	adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		replied <- struct{}{}
		return nil
	}}
	svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
	metadata := map[string]any{"command": "go test ./..."}
	svc.recordSafeCommandVerdict("session", commandHash(metadata), "tests are safe")
	svc.Ensure("opencode", adapter, "session", "cached-store-failure", "Bash", nil, metadata)

	select {
	case <-store.attempted:
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle persistence was not attempted")
	}
	select {
	case <-replied:
	case <-time.After(2 * time.Second):
		t.Fatal("cached permission was not auto-approved after lifecycle persistence failed")
	}
}

func TestServiceLifecycleUserResolutionMapping(t *testing.T) {
	for _, tt := range []struct {
		reply string
		want  state.PermissionResolution
	}{
		{reply: "reject", want: state.PermissionResolutionUserRejected},
		{reply: "cancel", want: state.PermissionResolutionCancelled},
		{reply: "cancelled", want: state.PermissionResolutionCancelled},
	} {
		t.Run(tt.reply, func(t *testing.T) {
			store := &lifecycleStore{enabled: true, exists: true}
			svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
			svc.Ensure("opencode", &fakePlatform{}, "session", tt.reply, "permission", nil, nil)
			store.waitFor(t, tt.reply, func(row state.PermissionLifecycle) bool { return row.RequestedAt > 0 })
			svc.HandleDirectPermissionReply(t.Context(), "session", tt.reply, tt.reply)
			store.waitFor(t, tt.reply, func(row state.PermissionLifecycle) bool { return row.Resolution == tt.want })
		})
	}

	store := &lifecycleStore{enabled: true, exists: true}
	svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
	svc.Ensure("opencode", &fakePlatform{}, "session", "unknown", "permission", nil, nil)
	store.waitFor(t, "unknown", func(row state.PermissionLifecycle) bool { return row.RequestedAt > 0 })
	svc.HandleDirectPermissionReply(t.Context(), "session", "unknown", "unexpected")
	time.Sleep(20 * time.Millisecond)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, update := range store.updates {
		if update.PermissionID == "unknown" && update.Resolution != "" {
			t.Fatalf("unknown reply persisted resolution %q", update.Resolution)
		}
	}
}

func (s *lifecycleStore) waitFor(t *testing.T, permissionID string, match func(state.PermissionLifecycle) bool) state.PermissionLifecycle {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var updates []state.PermissionLifecycle
	for time.Now().Before(deadline) {
		s.mu.Lock()
		updates = append([]state.PermissionLifecycle(nil), s.updates...)
		s.mu.Unlock()
		for _, update := range updates {
			if update.PermissionID == permissionID && match(update) {
				return update
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no matching lifecycle update for %s; updates=%+v", permissionID, s.updates)
	return state.PermissionLifecycle{}
}

func TestServiceLifecycleManualResolutionAndDisabledExclusion(t *testing.T) {
	store := &lifecycleStore{enabled: true, exists: true, delay: 5_000}
	svc := NewService(Deps{
		Store:      store,
		SessionDir: func(string) (string, error) { return "/repo/first", nil },
	})
	svc.SetJudgeDelayMs(5_000)
	svc.Ensure("opencode", &fakePlatform{}, "session", "enabled", "secret permission", nil, map[string]any{"secret": "metadata"})
	svc.HandleDirectPermissionReply(t.Context(), "session", "enabled", "reject")

	first := store.waitFor(t, "enabled", func(row state.PermissionLifecycle) bool { return row.RequestedAt > 0 })
	if first.Directory != "/repo/first" {
		t.Fatalf("first lifecycle snapshot = %+v", first)
	}
	store.waitFor(t, "enabled", func(row state.PermissionLifecycle) bool {
		return row.Resolution == state.PermissionResolutionUserRejected
	})
	store.waitFor(t, "enabled", func(row state.PermissionLifecycle) bool { return row.ManuallyPreempted })
	for _, tt := range []struct {
		reply string
		want  state.PermissionResolution
	}{
		{reply: "once", want: state.PermissionResolutionUserOnce},
		{reply: "always", want: state.PermissionResolutionUserAlways},
	} {
		svc.Ensure("opencode", &fakePlatform{}, "session", tt.reply, "permission", nil, nil)
		svc.HandleDirectPermissionReply(t.Context(), "session", tt.reply, tt.reply)
		store.waitFor(t, tt.reply, func(row state.PermissionLifecycle) bool { return row.Resolution == tt.want })
	}

	disabled := &lifecycleStore{enabled: false, exists: true}
	disabledSvc := NewService(Deps{Store: disabled, SessionDir: func(string) (string, error) { return "/repo", nil }})
	disabledSvc.Ensure("opencode", &fakePlatform{}, "session", "disabled", "permission", nil, nil)
	disabledSvc.HandleDirectPermissionReply(t.Context(), "session", "disabled", "once")
	time.Sleep(20 * time.Millisecond)
	disabled.mu.Lock()
	defer disabled.mu.Unlock()
	if len(disabled.updates) != 0 {
		t.Fatalf("disabled permission entered lifecycle: %+v", disabled.updates)
	}
}

func TestEnsureDoesNotBlockEventForwardingOnLifecycleLookups(t *testing.T) {
	block := make(chan struct{})
	store := &lifecycleStore{enabled: true, exists: true, getEnabledBlock: block}
	svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
	done := make(chan struct{})
	go func() {
		svc.Ensure("opencode", &fakePlatform{}, "session", "permission", "Edit", nil, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Ensure blocked on lifecycle settings lookup")
	}
	close(block)
}

func TestServiceLifecycleCacheAndDenylist(t *testing.T) {
	store := &lifecycleStore{enabled: true, exists: true}
	replied := make(chan struct{}, 1)
	adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		replied <- struct{}{}
		return nil
	}}
	svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
	metadata := map[string]any{"command": "go test ./..."}
	svc.recordSafeCommandVerdict("session", commandHash(metadata), "tests are safe")
	svc.Ensure("opencode", adapter, "session", "cached", "Bash", nil, metadata)
	select {
	case <-replied:
	case <-time.After(2 * time.Second):
		t.Fatal("cached permission was not auto-approved")
	}
	store.waitFor(t, "cached", func(row state.PermissionLifecycle) bool {
		return row.EvaluationMethod == state.PermissionEvaluationCache && row.EvaluationResult == state.PermissionEvaluationCacheSafe
	})
	store.waitFor(t, "cached", func(row state.PermissionLifecycle) bool {
		return row.Resolution == state.PermissionResolutionAutoApproved
	})

	svc.Ensure("opencode", adapter, "session", "denied", "Bash", nil, map[string]any{"command": "rm -rf /"})
	store.waitFor(t, "denied", func(row state.PermissionLifecycle) bool {
		return row.EvaluationMethod == state.PermissionEvaluationDenylist && row.EvaluationResult == state.PermissionEvaluationDenylisted
	})
	svc.HandleDirectPermissionReply(t.Context(), "session", "denied", "reject")
	row := store.waitFor(t, "denied", func(row state.PermissionLifecycle) bool {
		return row.Resolution == state.PermissionResolutionUserRejected
	})
	if row.ManuallyPreempted {
		t.Fatal("denylist resolution was marked manually preempted")
	}
}

func TestServiceLifecycleRecordsDirectoryResolutionFailure(t *testing.T) {
	store := &lifecycleStore{enabled: true, exists: true}
	svc := NewService(Deps{
		Store: store,
		SessionDir: func(string) (string, error) {
			return "", context.DeadlineExceeded
		},
	})
	svc.Ensure("opencode", &fakePlatform{}, "session", "no-directory", "Edit", nil, nil)

	store.waitFor(t, "no-directory", func(row state.PermissionLifecycle) bool {
		return row.EvaluationMethod == state.PermissionEvaluationJudge &&
			row.EvaluationResult == state.PermissionEvaluationError
	})
}

func TestServiceLifecycleJudgeResultMapping(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		failCreate bool
		wantResult state.PermissionEvaluationResult
	}{
		{name: "safe", response: `{"verdict":"safe","reasoning":"read only"}`, wantResult: state.PermissionEvaluationSafe},
		{name: "unsafe", response: `{"verdict":"unsafe","reasoning":"writes config"}`, wantResult: state.PermissionEvaluationUnsafe},
		{name: "unparseable", response: `not json`, wantResult: state.PermissionEvaluationError},
		{name: "transport", failCreate: true, wantResult: state.PermissionEvaluationError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			judgeServer := fakeLifecycleJudgeServer(t, tt.response, tt.failCreate)
			defer judgeServer.Close()
			u, _ := url.Parse(judgeServer.URL)
			store := &lifecycleStore{enabled: true, exists: true}
			svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
			svc.judge = &PermissionJudge{
				openCodePort:  func(string) string { return u.Port() },
				httpClient:    judgeServer.Client(),
				modelProvider: judgeModelProvider,
				modelID:       judgeModelID,
			}
			svc.Ensure("opencode", &fakePlatform{}, "session", tt.name, "Edit", nil, nil)
			row := store.waitFor(t, tt.name, func(row state.PermissionLifecycle) bool {
				return row.JudgeCompletedAt > 0 && row.EvaluationResult == tt.wantResult
			})
			started := store.waitFor(t, tt.name, func(row state.PermissionLifecycle) bool { return row.JudgeStartedAt > 0 })
			if started.EvaluationMethod != state.PermissionEvaluationJudge || started.JudgeStartedAt > row.JudgeCompletedAt {
				t.Fatalf("judge lifecycle = %+v", row)
			}
		})
	}
}

func TestServiceLifecycleKeepsLateVerdictWithoutAIReply(t *testing.T) {
	store := &lifecycleStore{enabled: true, exists: true}
	verdictReady := make(chan struct{})
	releaseVerdict := make(chan struct{})
	var replies atomic.Int32
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := `[]`
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			body = `{"id":"judge"}`
		case r.Method == http.MethodGet && r.URL.Path == "/session/judge/message":
			close(verdictReady)
			<-releaseVerdict
			body = `[{"info":{"role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"{\"verdict\":\"safe\",\"reasoning\":\"read only\"}"}]}]`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	svc := NewService(Deps{Store: store, SessionDir: func(string) (string, error) { return "/repo", nil }})
	svc.judge = &PermissionJudge{
		openCodePort:  func(string) string { return "1" },
		httpClient:    &http.Client{Transport: transport},
		modelProvider: judgeModelProvider,
		modelID:       judgeModelID,
	}
	adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		replies.Add(1)
		return nil
	}}
	svc.Ensure("opencode", adapter, "session", "late", "Edit", nil, nil)
	select {
	case <-verdictReady:
	case <-time.After(2 * time.Second):
		t.Fatal("judge did not reach verdict")
	}
	svc.HandleDirectPermissionReply(t.Context(), "session", "late", "reject")
	close(releaseVerdict)
	store.waitFor(t, "late", func(row state.PermissionLifecycle) bool {
		return row.JudgeCompletedAt > 0 && row.EvaluationResult == state.PermissionEvaluationSafe
	})
	store.waitFor(t, "late", func(row state.PermissionLifecycle) bool {
		return row.Resolution == state.PermissionResolutionUserRejected && row.ManuallyPreempted
	})
	if replies.Load() != 0 {
		t.Fatalf("late verdict sent %d AI replies", replies.Load())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeLifecycleJudgeServer(t *testing.T, response string, failCreate bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			if failCreate {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"id":"judge"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/session/message":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/judge/message":
			_, _ = w.Write([]byte(`[{"info":{"role":"assistant","finish":"stop"},"parts":[{"type":"text","text":` + quotedJSON(response) + `}]}]`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

func quotedJSON(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}
