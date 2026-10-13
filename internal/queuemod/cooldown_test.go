package queuemod

import (
	"context"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"net/http"
	"testing"
	"time"
)

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

type quotaPlatform struct{ platforms.Platform }

func (quotaPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	return &platforms.SessionDetail{Messages: []db.Message{{Data: []byte(`{"role":"assistant","providerID":"p","error":{"name":"APIError","data":{"statusCode":429}}}`)}}}, nil
}

type cooldownSessions struct {
	Sessions
	durations []time.Duration
}

func (s *cooldownSessions) RecordCooldown(_ context.Context, _ string, d time.Duration) {
	s.durations = append(s.durations, d)
}

func TestRetryResetIsOwnerQualified(t *testing.T) {
	svc := &cooldownSessions{}
	m := NewModule(Deps{
		Sessions: func() Sessions { return svc },
		Adapter:  func(context.Context, string, string) (platforms.Platform, bool) { return quotaPlatform{}, true },
	})
	m.OnSessionRetry("same", time.Now().Add(time.Minute).UnixMilli(), "")
	m.recordQuotaCooldown(t.Context(), "r-other:opencode", "same")
	m.recordQuotaCooldown(t.Context(), "opencode", "same")
	if len(svc.durations) != 2 || svc.durations[0] != 0 || svc.durations[1] <= 0 {
		t.Fatalf("reset durations = %v; remote must not consume local retry reset", svc.durations)
	}
}
