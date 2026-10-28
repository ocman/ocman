package claudecode

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestUnboundSessionCannotFallBackAfterFailedActualOwnerProbe(t *testing.T) {
	c, old, _, _ := sessionRouteFixture(t, db.StatusBusy)
	failed, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "actual owner unavailable", http.StatusServiceUnavailable)
	})
	c.roots["/unrelated-project"] = &claudeProject{watched: watched{adapter: failed}}
	// No manager is installed: fallback must fail before it can ensure a stale
	// project runtime, not launch another child with the same native identity.
	for _, launch := range []bool{false, true} {
		a, _, err := c.route(t.Context(), "moved-session", launch)
		if !errors.Is(err, platforms.ErrPlatformUnreachable) || a != nil {
			t.Fatalf("launch=%v: incomplete discovery selected stale owner=%v: %v", launch, a == old, err)
		}
	}
	if c.sessions["moved-session"] != nil {
		t.Fatal("failed discovery installed a runtime binding")
	}
}

func TestLocalSessionDiscoveryTimeoutIsNotAbsence(t *testing.T) {
	c, _, _, _ := sessionRouteFixture(t, db.StatusBusy)
	stalled, _ := reloadTestAdapter(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c.roots["/unrelated-project"] = &claudeProject{watched: watched{adapter: stalled}}
	ctx := t.Context()
	if a, _, err := c.route(ctx, "moved-session", false); a != nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("local discovery deadline became a stale runtime selection: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("fixture cancelled the parent instead of exercising the local deadline")
	}
}
