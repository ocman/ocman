package opencode

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestCatalogFailureWarningsCoalesceWithoutStoppingRetries(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewLocal(logger)

	// Separate sessions/directories can hit one failing machine server.
	for _, sid := range []string{"one", "two", "three"} {
		agentCatalogAt(context.Background(), port, sid, "/project/"+sid)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("upstream requests = %d, want 3: failures must still retry", got)
	}
	var warnings int
	for _, entry := range hook.AllEntries() {
		if entry.Level == log.WarnLevel {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("warnings = %d, want 1 for the same upstream failure", warnings)
	}
}

func TestConcurrentCatalogFailureWarningsCoalesce(t *testing.T) {
	fetchFailureLogs.reset()
	t.Cleanup(fetchFailureLogs.reset)
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewLocal(logger)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			logFetchFailure(context.DeadlineExceeded, log.Fields{"port": "concurrent-test", "endpoint": "/agent"}, "catalog failed")
		})
	}
	wg.Wait()
	if got := len(hook.AllEntries()); got != 1 {
		t.Fatalf("log entries = %d, want 1", got)
	}
}

func TestFetchFailureWarningIdentityAndExpiry(t *testing.T) {
	fetchFailureLogs.reset()
	t.Cleanup(fetchFailureLogs.reset)
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewLocal(logger)
	err := errors.New("upstream unavailable")
	fields := log.Fields{"port": "warning-test", "endpoint": "/agent", "sessionID": "one"}
	warn := func() { logFetchFailure(err, fields, "catalog failed") }
	warn()
	fields["sessionID"] = "two"
	warn() // Session changes on the same machine do not repeat a warning.
	if got := len(hook.AllEntries()); got != 1 {
		t.Fatalf("same upstream: entries = %d, want 1", got)
	}
	fetchFailureLogs.mu.Lock()
	for key, entry := range fetchFailureLogs.entries {
		entry.expiresAt = time.Now().Add(-time.Second)
		fetchFailureLogs.entries[key] = entry
	}
	fetchFailureLogs.mu.Unlock()
	warn()
	fields["endpoint"] = "/command"
	warn()
	fields["port"] = "other-machine"
	warn()
	logFetchFailure(errors.New("new failure"), fields, "catalog failed")
	fields["path"] = "/provider"
	warn()
	delete(fields, "port")
	warn()
	fields["sessionID"] = "three"
	warn() // Without a port, unrelated session failures stay distinct.
	if got := len(hook.AllEntries()); got != 8 {
		t.Fatalf("distinct or expired failures: entries = %d, want 8", got)
	}
}

func TestFetchFailureWarningCancellationAndSharedFailure(t *testing.T) {
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	oldLevel := logger.GetLevel()
	logger.SetLevel(log.DebugLevel)
	t.Cleanup(func() { logger.SetLevel(oldLevel) })
	hook := logtest.NewLocal(logger)
	for _, err := range []error{context.Canceled, errFetchFailed} {
		logFetchFailure(err, log.Fields{"port": "quiet-test"}, "catalog failed")
	}
	if got := len(hook.AllEntries()); got != 2 {
		t.Fatalf("debug entries = %d, want 2", got)
	}
	for _, entry := range hook.AllEntries() {
		if entry.Level != log.DebugLevel {
			t.Fatalf("level = %v, want DEBUG", entry.Level)
		}
	}
}

func TestCatalogFailureWarningInvalidation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate func()
	}{
		{"catalog reload", func() { InvalidateCatalogsForPort("invalidation-test") }},
		{"backend reset", ResetCaches},
		{"port disappeared", func() { New(nil, nil, nil, ocapi.Auth{}).ClearPromptsForPort("invalidation-test") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetchFailureLogs.reset()
			t.Cleanup(fetchFailureLogs.reset)
			logger := log.StandardLogger()
			oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
			t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
			hook := logtest.NewLocal(logger)
			warn := func() {
				logFetchFailure(context.DeadlineExceeded, log.Fields{"port": "invalidation-test", "endpoint": "/agent"}, "catalog failed")
			}
			warn()
			warn()
			tc.invalidate()
			warn()
			if got := len(hook.AllEntries()); got != 2 {
				t.Fatalf("entries = %d, want 2 across invalidation", got)
			}
		})
	}
}
