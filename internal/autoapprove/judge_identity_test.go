package autoapprove

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
)

type canonicalJudgePlatform struct {
	platforms.Platform
	id  string
	err error
}

func (*canonicalJudgePlatform) CanonicalSessionIDs() bool { return true }
func (p *canonicalJudgePlatform) HelperSessionIdentity(_ context.Context, native string) (string, error) {
	if native != "native-judge" {
		return "", platforms.ErrNotFound
	}
	return p.id, p.err
}

func TestJudgeSessionCallbackUsesCanonicalIdentityButCleanupRemainsNative(t *testing.T) {
	var deleted, prompted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = w.Write([]byte(`{"id":"native-judge"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/session/native-judge":
			deleted.Store(true)
			_, _ = w.Write([]byte(`true`))
		case r.URL.Path == "/session/native-judge/prompt_async":
			prompted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/session/native-judge/message":
			_, _ = w.Write([]byte(`[{"info":{"role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"{\"verdict\":\"safe\",\"reasoning\":\"ok\"}"}]}]`))
		default:
			t.Errorf("judge execution received non-native route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	p := &canonicalJudgePlatform{id: "ocm_" + uuid.NewString()}
	svc := NewService(Deps{OpencodePlatform: func() platforms.Platform { return p }})
	svc.judge.openCodePort = func(string) string { return strings.TrimPrefix(server.URL, "http://127.0.0.1:") }
	var observed string
	result := svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, func(id string) { observed = id })
	if result.Verdict != verdictSafe || result.EvaluationFailed || observed != p.id || !deleted.Load() || !prompted.Load() {
		t.Fatalf("judge identity boundary failed: result=%+v observed=%q deleted=%v prompted=%v", result, observed, deleted.Load(), prompted.Load())
	}
	for _, failure := range []struct {
		id  string
		err error
	}{{p.id, platforms.ErrHistoryUnavailable}, {"native", nil}} {
		p.id, p.err = failure.id, failure.err
		deleted.Store(false)
		prompted.Store(false)
		observed = ""
		result = svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, func(id string) { observed = id })
		if result.Verdict == verdictSafe || !result.EvaluationFailed || observed != "" || !deleted.Load() || prompted.Load() {
			t.Fatalf("failed adoption leaked authority or skipped native cleanup: %+v %q", result, observed)
		}
	}
}
