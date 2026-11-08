package webhook

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ocman/ocman/internal/share"
)

func TestPollerBoundsDefaultRelayRequests(t *testing.T) {
	f := newPollingFixture(t)
	f.poller.HTTP = nil
	previous := http.DefaultTransport
	http.DefaultTransport = webhookRoundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		started := time.Now()
		if err := f.poller.Poll(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stalled relay request: %v", err)
		}
		if elapsed := time.Since(started); elapsed != share.RequestTimeout || ctx.Err() != nil {
			t.Fatalf("request consumed the caller lifetime: elapsed=%s caller=%v", elapsed, ctx.Err())
		}
	})
}

func TestPollerCancelsStuckDispatchAndRetriesWithoutRestart(t *testing.T) {
	f := newPollingFixture(t)
	f.deliver()
	f.poller.Routines = webhookDispatchFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		started := time.Now()
		if err := f.poller.Poll(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stuck dispatch: %v", err)
		}
		if elapsed := time.Since(started); elapsed != 2*time.Minute || ctx.Err() != nil || !f.pending || f.acks != 0 {
			t.Fatalf("iteration did not cancel safely: elapsed=%s caller=%v pending=%v acks=%d", elapsed, ctx.Err(), f.pending, f.acks)
		}
		f.poller.Routines = &fakeRoutines{}
		if err := f.poller.Poll(ctx); err != nil || f.pending || f.acks != 1 {
			t.Fatalf("cancelled iteration did not retry: pending=%v acks=%d err=%v", f.pending, f.acks, err)
		}
	})
}

func TestWebhookHistoryCleanupHasIndependentDeadline(t *testing.T) {
	m, _ := testModule(t)
	held, err := m.store.SQL().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		defer cancel()
		started := time.Now()
		m.cleanHistory(ctx)
		if elapsed := time.Since(started); elapsed != time.Minute || ctx.Err() != nil {
			t.Fatalf("cleanup consumed module lifetime: elapsed=%s caller=%v", elapsed, ctx.Err())
		}
	})
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	m.cleanHistory(t.Context())
}
