package autoapprove

import (
	"context"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

type reviewerAccount struct {
	*platformtest.Fake
	client *http.Client
}

func (p reviewerAccount) OpenCodeEventClient() *http.Client { return p.client }

func TestReviewerAccountDoesNotChangeWatchedPlatform(t *testing.T) {
	watched := &platformtest.Fake{PlatformID: "opencode"}
	client := &http.Client{}
	selected := reviewerAccount{Fake: &platformtest.Fake{PlatformID: "opencode"}, client: client}
	svc := NewService(Deps{OpencodePlatform: func() platforms.Platform { return watched }, ReviewerPlatform: func() platforms.Platform { return selected }})
	if svc.OpencodeAdapter() != watched {
		t.Fatal("reviewer changed watched account")
	}
	if svc.reviewerHTTPClient() != client || svc.judge.openCodeHTTPClient() != client {
		t.Fatal("reviewer did not use its authenticated transport")
	}
	if svc.reviewerPort("/repo") != "" {
		t.Fatal("invented reviewer port")
	}
	svc.deps.ReviewerPlatform = func() platforms.Platform { return nil }
	response, err := svc.reviewerHTTPClient().Get("http://127.0.0.1:1")
	if response != nil { _ = response.Body.Close() }
	if err == nil {
		t.Fatal("unavailable reviewer borrowed another transport")
	}
}

func TestNamedJudgeModelStripsOnlyAccountPrefix(t *testing.T) {
	provider, model, ok := loadJudgeModel(context.Background(), fakeSettingStore{val: "cnx_work:openrouter/anthropic/haiku", ok: true})
	if !ok || provider != "openrouter" || model != "anthropic/haiku" {
		t.Fatalf("model = %s/%s, %v", provider, model, ok)
	}
}

type namedReviewerStore struct{ contextSettingStore }

func (*namedReviewerStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	if key == JudgeModelSettingKey {
		return "cnx_missing:p/m", true, nil
	}
	return "", false, nil
}

type forbiddenReviewerFallback struct{ t *testing.T }

func (f forbiddenReviewerFallback) JudgeWithCallback(context.Context, string, string, []string, map[string]any, []PromptSection, func(string)) JudgeResult {
	f.t.Fatal("unavailable named reviewer borrowed a fallback account")
	return JudgeResult{}
}

func TestUnavailableNamedReviewerDoesNotUseFallback(t *testing.T) {
	svc := NewService(Deps{Store: &namedReviewerStore{}, ReviewerPlatform: func() platforms.Platform { return nil }, FallbackJudge: forbiddenReviewerFallback{t}})
	result := svc.judgePermission(t.Context(), "cnx_session", "/repo", "bash", nil, nil, nil, nil)
	if result.Verdict == verdictSafe {
		t.Fatal("unavailable reviewer approved permission")
	}
}
