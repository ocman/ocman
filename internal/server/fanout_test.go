package server

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestFanOutSessions_SlowRemoteDoesNotBlock(t *testing.T) {
	srv := testServer(t)

	// A fast local-shaped adapter and a slow remote-shaped one. The
	// remote sleeps well past the fan-out timeout; the aggregate must
	// still return promptly with the fast adapter's sessions.
	srv.registry.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "local1", Platform: "fake"}},
	})
	srv.registry.Register(&fakePlatform{
		id: "r-slow:opencode",
		sessionsHook: func(ctx context.Context, _ string, _ int64) ([]db.Session, error) {
			select {
			case <-time.After(10 * time.Second):
				return []db.Session{{ID: "slow1", Platform: "r-slow:opencode"}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})

	start := time.Now()
	all := srv.fanOutSessions(context.Background(), "", 0, nil)
	elapsed := time.Since(start)

	if elapsed > remoteFanoutTimeout+2*time.Second {
		t.Fatalf("fan-out blocked on slow remote: %v", elapsed)
	}
	// The fast adapter's session is present; the slow one is absent.
	var haveLocal, haveSlow bool
	for _, s := range all {
		switch s.ID {
		case "local1":
			haveLocal = true
		case "slow1":
			haveSlow = true
		}
	}
	if !haveLocal {
		t.Error("fast adapter session missing from aggregate")
	}
	if haveSlow {
		t.Error("slow remote should have been dropped by the timeout")
	}
}

func TestIsLocalAdapter(t *testing.T) {
	if !isLocalAdapter(&fakePlatform{id: "opencode"}) {
		t.Error("bare id should be local")
	}
	if isLocalAdapter(&fakePlatform{id: "r-abc:opencode"}) {
		t.Error("compound id should be remote")
	}
}
