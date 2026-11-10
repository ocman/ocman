package queuemod

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

type scopedCooldownSessions struct {
	cooldownSessions
	ids, providers []string
}

func (*scopedCooldownSessions) Fallthrough(context.Context, string, string) (string, time.Time, bool) {
	return "", time.Time{}, false
}

func (s *scopedCooldownSessions) RecordSessionCooldown(_ context.Context, id, provider string, _ time.Duration) {
	s.ids = append(s.ids, id)
	s.providers = append(s.providers, provider)
}

func TestQuotaCooldownPassesSessionIdentity(t *testing.T) {
	svc := &scopedCooldownSessions{}
	m := NewModule(Deps{
		Sessions: func() Sessions { return svc },
		Adapter:  func(context.Context, string, string) (platforms.Platform, bool) { return quotaPlatform{}, true },
	})
	m.OnSessionRetry("cnx_work", "retry-session", 0, "account_rate_limit")
	m.recordQuotaCooldown(t.Context(), "cnx_personal", "idle-session")
	if len(svc.ids) != 2 || svc.ids[0] != "retry-session" || svc.ids[1] != "idle-session" ||
		svc.providers[0] != "p" || svc.providers[1] != "p" || len(svc.durations) != 0 {
		t.Fatalf("scoped recording = %v %v, global = %v", svc.ids, svc.providers, svc.durations)
	}
	m.recordCooldown(t.Context(), "ignored", "", time.Hour)
	if len(svc.ids) != 2 {
		t.Fatal("empty provider recorded")
	}
}
