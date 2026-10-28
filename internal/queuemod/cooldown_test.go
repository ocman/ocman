package queuemod

import (
	"context"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
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

type cancelledSelection struct{ Sessions }

func (cancelledSelection) Fallthrough(context.Context, string, string) (string, time.Time, bool) {
	return "", time.Time{}, false
}

func TestCancelledModelSelectionKeepsIdleEdgeHeld(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m := NewModule(Deps{Sessions: func() Sessions { return cancelledSelection{} }})
	if !m.continueSession(ctx, "opencode", "s1", "provider") {
		t.Fatal("cancelled model selection fell through to a queue drain")
	}
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
	m.OnSessionRetry("claudecode", "same", time.Now().Add(time.Minute).UnixMilli(), "")
	m.recordQuotaCooldown(t.Context(), "r-other:claudecode", "same")
	m.recordQuotaCooldown(t.Context(), "opencode", "same")
	m.recordQuotaCooldown(t.Context(), "claudecode", "same")
	if len(svc.durations) != 3 || svc.durations[0] != 0 || svc.durations[1] != 0 || svc.durations[2] <= 0 {
		t.Fatalf("reset durations = %v; only the retrying owner may consume its retry reset", svc.durations)
	}
}

func TestFallNoticeAnnouncesOwnerPlatform(t *testing.T) {
	var changed []string
	m := NewModule(Deps{SessionChanged: func(platformID, sessionID string) { changed = append(changed, platformID+"/"+sessionID) }})
	m.setExhaustedNotice("claudecode", "s1", time.Now().Add(time.Minute))
	if len(changed) != 1 || changed[0] != "claudecode/s1" {
		t.Fatalf("session changes = %v, want claudecode/s1", changed)
	}
	sess := &db.Session{ID: "s1"}
	m.ApplyFallNotice("opencode", sess)
	if sess.Notice != nil {
		t.Fatal("another owner's session received the notice")
	}
	m.ApplyFallNotice("claudecode", sess)
	if sess.Notice == nil || sess.Notice.Kind != "models_exhausted" {
		t.Fatalf("notice = %+v", sess.Notice)
	}
}
