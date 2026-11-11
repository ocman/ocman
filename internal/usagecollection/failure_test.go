package usagecollection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type reportTransport func(*http.Request) (*http.Response, error)

func (f reportTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeliveryFailuresKeepSettingsUsableAndRetryInBackground(t *testing.T) {
	for _, status := range []int{0, 401, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m, _ := fixture(t)
			calls := 0
			m.client.Transport = reportTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if status == 0 {
					return nil, errors.New("connection unavailable")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("private collector error")), Header: make(http.Header)}, nil
			})
			m.RecordActivity(t.Context())
			m.sendPending(t.Context())
			pending, err := m.pending(t.Context(), m.now())
			if err != nil || pending == nil {
				t.Fatalf("failed delivery must remain retryable: %v, %v", pending, err)
			}
			if w := setting(t, m, http.MethodGet, ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "collector error") {
				t.Fatalf("delivery failure leaked into settings: %d %s", w.Code, w.Body.String())
			}
			m.RecordActivity(t.Context())
			m.sendPending(t.Context())
			if calls != 1 {
				t.Fatalf("foreground activity bypassed retry backoff: %d calls", calls)
			}
			now := m.now()
			m.now = func() time.Time { return now.Add(15 * time.Minute) }
			m.client.Transport = reportTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Header: make(http.Header)}, nil
			})
			m.sendPending(t.Context())
			if pending, err := m.pending(t.Context(), m.now()); err != nil || pending != nil || calls != 2 {
				t.Fatalf("background retry did not recover: %v, %v, calls=%d", pending, err, calls)
			}
		})
	}
}

func TestStalledDeliveryDoesNotBlockActivitySettingsOrShutdown(t *testing.T) {
	m, _ := fixture(t)
	started := make(chan struct{})
	m.client.Transport = blockedTransport{started}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("shutdown waited for stalled metrics delivery")
		}
	})
	m.RecordActivity(t.Context())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background delivery did not start")
	}
	foreground := make(chan int, 1)
	go func() {
		m.RecordActivity(t.Context())
		foreground <- setting(t, m, http.MethodGet, "").Code
	}()
	select {
	case code := <-foreground:
		if code != http.StatusOK {
			t.Fatalf("settings status = %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground work waited for metrics delivery")
	}
}

func TestStalledDeliveryTimesOutWithoutAcknowledgingReport(t *testing.T) {
	m, _ := fixture(t)
	if m.client.Timeout != 5*time.Second {
		t.Fatalf("delivery timeout = %v, want five seconds", m.client.Timeout)
	}
	// Exercise HTTP client cancellation without waiting five seconds in tests.
	m.client.Timeout = 20 * time.Millisecond
	m.client.Transport = blockedTransport{make(chan struct{})}
	m.RecordActivity(t.Context())
	done := make(chan struct{})
	go func() { m.sendPending(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stalled metrics delivery did not time out")
	}
	if pending, err := m.pending(t.Context(), m.now()); err != nil || pending == nil {
		t.Fatalf("timeout must preserve pending report: %v, %v", pending, err)
	}
}
