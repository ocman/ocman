package linkpreview

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
)

func previewTestService(roundTrip ogTransport) *Service {
	return New(&fakeTokens{grants: map[string]string{"alice/w1": "token"}},
		&http.Client{Transport: roundTrip}, &mockResolver{base: "https://api.example.com"})
}

func requestTestPreview(ctx context.Context, svc *Service, id string) Preview {
	return svc.Resolve(ctx, "alice", "owner", []Ref{{Provider: "mock", Kind: "issue", ID: id}})[0]
}

func successfulTestPreview() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(
		strings.NewReader(`{"title":"Preview","url":"https://tracker.example.com/browse/ABC-1"}`),
	)}
}

func TestQueuedPreviewObservesProviderCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limited, release := make(chan struct{}), make(chan struct{})
		calls := make(chan string, maxInFlight+1)
		svc := previewTestService(func(r *http.Request) (*http.Response, error) {
			calls <- r.URL.Path
			switch r.URL.Path {
			case "/issues/ABC-429":
				<-limited
				return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody}, nil
			case "/issues/ABC-999":
				return successfulTestPreview(), nil
			default:
				<-release
				return successfulTestPreview(), nil
			}
		})
		defer close(release)
		for i := range maxInFlight - 1 {
			go requestTestPreview(t.Context(), svc, fmt.Sprintf("ABC-%d", i))
		}
		go requestTestPreview(t.Context(), svc, "ABC-429")
		synctest.Wait()
		queued := make(chan Preview, 1)
		go func() { queued <- requestTestPreview(t.Context(), svc, "ABC-999") }()
		synctest.Wait()
		before := len(calls)
		close(limited) // Releases exactly one slot, after recording the cooldown.
		got := <-queued
		if before != maxInFlight || got.State != StateRateLimited || len(calls) != before {
			t.Fatalf("queued preview state = %s, provider calls = %d before and %d after 429; want rate_limited and %d calls throughout", got.State, before, len(calls), maxInFlight)
		}
	})
}

func TestConcurrentPreviewReadersShareBudgetCharge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}, 4), make(chan struct{})
		svc := previewTestService(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/issues/ABC-999" {
				started <- struct{}{}
				<-release
			}
			return successfulTestPreview(), nil
		})
		for i := range fetchesPerMinute - 2 {
			if p := requestTestPreview(t.Context(), svc, fmt.Sprintf("ABC-%d", i)); p.State != StateOK {
				t.Fatalf("prefill request %d = %s", i, p.State)
			}
		}
		results := make(chan Preview, 4)
		ctx := t.Context()
		for range cap(results) {
			go func() { results <- requestTestPreview(ctx, svc, "ABC-999") }()
		}
		synctest.Wait() // All readers are either waiting for the leader or rejected.
		close(release)
		for range cap(results) {
			if got := <-results; got.State != StateOK {
				t.Errorf("shared reader = %s, want ok", got.State)
			}
		}
		if got := requestTestPreview(ctx, svc, "ABC-1000"); got.State != StateOK || len(started) != 1 {
			t.Errorf("final budgeted preview = %s, shared provider calls = %d; want ok and one call", got.State, len(started))
		}
		if got := requestTestPreview(ctx, svc, "ABC-1001"); got.State != StateRateLimited {
			t.Errorf("preview beyond budget = %s, want rate_limited", got.State)
		}
	})
}
