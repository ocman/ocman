package webhook

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestPollerBacksOffAndLogsPersistentRelayFailureOnce(t *testing.T) {
	f := newPollingFixture(t)
	previous := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(previous) })
	hook := logtest.NewGlobal()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var attempts []time.Duration
		start := time.Now()
		f.poller.HTTP.Transport = webhookRoundTrip(func(r *http.Request) (*http.Response, error) {
			attempts = append(attempts, time.Since(start))
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("SECRET relay response")), Header: make(http.Header), Request: r}, nil
		})
		go f.poller.Run(ctx)
		time.Sleep(2 * time.Minute)
		cancel()
		synctest.Wait()
		want := []time.Duration{0, 15 * time.Second, 45 * time.Second, 105 * time.Second}
		if len(attempts) != len(want) {
			t.Fatalf("persistent failure made %d requests, want %d with backoff: %v", len(attempts), len(want), attempts)
		}
		for i := range want {
			if attempts[i] != want[i] {
				t.Fatalf("attempt %d at %v, want %v", i, attempts[i], want[i])
			}
		}
	})
	entries := hook.AllEntries()
	if len(entries) != 1 || entries[0].Data["status"] != http.StatusForbidden {
		t.Fatalf("want one warning with safe HTTP status, got %+v", entries)
	}
	text, err := entries[0].String()
	if err != nil || strings.Contains(text, "SECRET") || strings.Contains(text, "relay.invalid") {
		t.Fatalf("warning leaked relay details: %q %v", text, err)
	}
}

func TestPollerBackoffCapsAndResetsAfterRecovery(t *testing.T) {
	f := newPollingFixture(t)
	previous := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(previous) })
	hook := logtest.NewGlobal()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := time.Now()
		var attempts []time.Duration
		f.poller.HTTP.Transport = webhookRoundTrip(func(r *http.Request) (*http.Response, error) {
			attempts = append(attempts, time.Since(start))
			status, body := http.StatusServiceUnavailable, "SECRET"
			if len(attempts) == 8 {
				status, body = http.StatusOK, `{"deliveries":[]}`
			}
			if len(attempts) == 11 {
				cancel()
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
		})
		observer := webhookScheduleFunc(func(ctx context.Context, _ module.Schedule, work func(context.Context) error) error {
			err := work(ctx)
			if err != nil && err.Error() != "webhook inbox polling failed: HTTP 503" {
				t.Fatalf("unexpected scheduled error: %v", err)
			}
			return err
		})
		f.poller.Run(module.WithScheduleObserver(ctx, observer))
		want := []time.Duration{0, 15, 45, 105, 225, 465, 765, 1065, 1080, 1095, 1125}
		if len(attempts) != len(want) {
			t.Fatalf("attempts: %v", attempts)
		}
		for i, seconds := range want {
			if attempts[i] != seconds*time.Second {
				t.Fatalf("attempt %d at %v, want %v seconds", i, attempts[i], seconds)
			}
		}
	})
	if entries := hook.AllEntries(); len(entries) != 2 {
		t.Fatalf("want one warning per outage, got %+v", entries)
	}
}
