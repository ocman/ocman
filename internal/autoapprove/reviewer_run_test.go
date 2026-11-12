package autoapprove

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
)

type runReviewerAccount struct {
	*canonicalJudgePlatform
	port   string
	client *http.Client
}

func (p *runReviewerAccount) InstancePorts() platforms.InstancePorts { return p }
func (p *runReviewerAccount) DiscoverOpenCodePorts() map[string]string {
	return map[string]string{"/repo": p.port}
}
func (p *runReviewerAccount) DiscoverOpenCodePort(string) string { return p.port }
func (p *runReviewerAccount) OpenCodeEventClient() *http.Client  { return p.client }

func TestReviewerSelectionRecoversWithoutRebuildingService(t *testing.T) {
	var selected atomic.Pointer[runReviewerAccount]
	var deleted atomic.Bool
	account := newRunReviewerAccount(t, "a", &deleted, nil)
	svc := NewService(Deps{OpenCodeDisabled: true, ReviewerPlatform: func() platforms.Platform {
		if p := selected.Load(); p != nil {
			return p
		}
		return nil
	}})
	judge := func() JudgeResult {
		return svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, nil)
	}
	if result := judge(); result.Verdict == verdictSafe {
		t.Fatal("missing reviewer approved")
	}
	selected.Store(account)
	svc.ReloadJudgeModel(context.Background())
	if result := judge(); result.Verdict != verdictSafe || result.EvaluationFailed || !deleted.Load() {
		t.Fatalf("healthy reviewer did not recover: %+v, deleted=%v", result, deleted.Load())
	}
}

func TestReviewerRunDoesNotBorrowLegacyTransport(t *testing.T) {
	var deleted atomic.Bool
	account := newRunReviewerAccount(t, "a", &deleted, nil)
	account.client = nil
	svc := NewService(Deps{ReviewerPlatform: func() platforms.Platform { return account }, OpenCodeAuth: ocapi.New("a")})
	result := svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, nil)
	if result.Verdict == verdictSafe || !result.EvaluationFailed || deleted.Load() {
		t.Fatalf("missing named transport borrowed legacy credentials: %+v", result)
	}
	legacy := NewService(Deps{OpenCodeDisabled: true})
	if legacy.judge.openCodePort != nil {
		t.Fatal("disabled legacy judge was enabled")
	}
}

func TestReviewerRunPinsAccountThroughCleanup(t *testing.T) {
	var selected atomic.Pointer[runReviewerAccount]
	var deletedA, deletedB atomic.Bool
	b := newRunReviewerAccount(t, "b", &deletedB, nil)
	a := newRunReviewerAccount(t, "a", &deletedA, func() { selected.Store(b) })
	selected.Store(a)
	var resolutions atomic.Int32
	svc := NewService(Deps{ReviewerPlatform: func() platforms.Platform {
		resolutions.Add(1)
		return selected.Load()
	}})
	var observed string
	result := svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, func(id string) {
		observed = id
		svc.judge.setModel("changed", "model")
	})
	if result.Verdict != verdictSafe || result.EvaluationFailed || observed != a.id || !deletedA.Load() || deletedB.Load() {
		t.Fatalf("reviewer changed during run: %+v, identity=%s deletedA=%v deletedB=%v", result, observed, deletedA.Load(), deletedB.Load())
	}
	if resolutions.Load() != 1 {
		t.Fatalf("resolved account %d times in one run", resolutions.Load())
	}
	svc.judge.setModel(judgeModelProvider, judgeModelID)
	result = svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, func(id string) { observed = id })
	if result.Verdict != verdictSafe || observed != b.id || !deletedB.Load() {
		t.Fatalf("next run did not use B: %+v %s", result, observed)
	}
}

func newRunReviewerAccount(t *testing.T, password string, deleted *atomic.Bool, created func()) *runReviewerAccount {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, got, ok := r.BasicAuth()
		if !ok || got != password {
			http.Error(w, "wrong account", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/info":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			if created != nil {
				created()
			}
			_, _ = w.Write([]byte(`{"id":"native-judge"}`))
		case r.Method == http.MethodDelete:
			deleted.Store(true)
			_, _ = w.Write([]byte(`true`))
		case strings.HasSuffix(r.URL.Path, "/prompt_async"):
			var prompt struct {
				Model struct {
					ProviderID string `json:"providerID"`
					ModelID    string `json:"modelID"`
				} `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&prompt); err != nil {
				t.Error(err)
			}
			if prompt.Model.ProviderID != judgeModelProvider || prompt.Model.ModelID != judgeModelID {
				t.Errorf("model changed during run: %+v", prompt.Model)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`[{"info":{"role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"{\"verdict\":\"safe\",\"reasoning\":\"ok\"}"}]}]`))
		}
	}))
	t.Cleanup(server.Close)
	return &runReviewerAccount{
		canonicalJudgePlatform: &canonicalJudgePlatform{id: "ocm_00000000-0000-4000-8000-00000000000" + map[string]string{"a": "1", "b": "2"}[password]},
		port:                   strings.TrimPrefix(server.URL, "http://127.0.0.1:"),
		client:                 &http.Client{Transport: ocapi.New(password).IsolatedTransport(http.DefaultTransport)},
	}
}
