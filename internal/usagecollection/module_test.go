package usagecollection

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/usagewire"
)

func fixture(t *testing.T) (*Module, *[]usagewire.Report) {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reports := &[]usagewire.Report{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" || r.Method != http.MethodPost {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials must not be forwarded")
		}
		var report usagewire.Report
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		*reports = append(*reports, report)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	m := NewModule(Deps{Store: db, RelayURL: func() string { return server.URL }, Version: func() string { return "v0.275.0" }})
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	return m, reports
}

func setting(t *testing.T, m *Module, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	return moduletest.Do(moduletest.Mount(m), method, "/api/settings/usage-collection", body, "")
}

func TestDailyActivityAndStableIdentity(t *testing.T) {
	m, reports := fixture(t)
	if w := setting(t, m, "GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	m.sendPending(t.Context())
	if len(*reports) != 0 {
		t.Fatal("startup is not activity")
	}
	m.RecordActivity(t.Context())
	m.sendPending(t.Context())
	if len(*reports) != 1 || (*reports)[0].Version != "0.275.0" || (*reports)[0].Date != "2026-11-11" {
		t.Fatalf("reports=%v", *reports)
	}
	id := (*reports)[0].InstallationID
	m.nextAttempt = time.Time{}
	m.RecordActivity(t.Context())
	m.sendPending(t.Context())
	if len(*reports) != 1 {
		t.Fatal("reported twice in one day")
	}
	// Recreate the module over the same durable state to simulate a restart.
	restarted := NewModule(m.deps)
	restarted.now = m.now
	restarted.RecordActivity(t.Context())
	restarted.sendPending(t.Context())
	if len(*reports) != 1 {
		t.Fatal("restart bypassed daily deduplication")
	}
	restarted.now = func() time.Time { return m.now().AddDate(0, 0, 1) }
	restarted.RecordActivity(t.Context())
	restarted.sendPending(t.Context())
	if len(*reports) != 2 || (*reports)[1].InstallationID != id {
		t.Fatalf("identity rotated: %v", *reports)
	}
}

func TestOptOutClearsPendingAndDoesNotBackfill(t *testing.T) {
	m, reports := fixture(t)
	m.RecordActivity(t.Context())
	if w := setting(t, m, "POST", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	m.RecordActivity(t.Context())
	m.sendPending(t.Context())
	if len(*reports) != 0 {
		t.Fatal("sent opted-out activity")
	}
	if w := setting(t, m, "POST", `{"enabled":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	m.sendPending(t.Context())
	if len(*reports) != 0 {
		t.Fatal("backfilled opted-out or cleared activity")
	}
	m.RecordActivity(t.Context())
	m.sendPending(t.Context())
	if len(*reports) != 1 {
		t.Fatal("new activity not reported")
	}
}

func TestSettingsInvalidAndUnavailable(t *testing.T) {
	m, _ := fixture(t)
	for _, body := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":"yes"}`, `{"enabled":true,"prompt":"secret"}`, `{} {}`} {
		if w := setting(t, m, "POST", body); w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	if w := setting(t, m, "DELETE", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := setting(t, NewModule(Deps{}), "GET", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	none := NewModule(Deps{})
	none.RecordActivity(t.Context())
	none.sendPending(t.Context())
	if none.Name() != "usage-collection" {
		t.Fatal(none.Name())
	}
	if err := m.deps.Store.SetSetting(t.Context(), EnabledSetting, "invalid"); err != nil {
		t.Fatal(err)
	}
	if w := setting(t, m, "GET", ""); w.Code != 500 {
		t.Fatal(w.Code)
	}
}

func TestEndpointPrivacy(t *testing.T) {
	for _, input := range []string{"", "https://user:pass@example.com", "https://example.com?secret=x", "https://example.com#secret", "https://example.com/path", "http://public.example.com", "file:///tmp/reports", "://"} {
		if got := endpoint(input); got != "" {
			t.Fatalf("accepted %q: %q", input, got)
		}
	}
	for _, input := range []string{"https://relay.example.com", "http://localhost:4318/", "http://127.0.0.1:4318", "http://[::1]:4318"} {
		if !strings.HasSuffix(endpoint(input), "/v1/usage") {
			t.Fatal(input)
		}
	}
}

type failingTransport struct{ calls atomic.Int32 }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls.Add(1)
	return nil, errors.New("offline")
}

func TestFailureBackoffAndStaleActivity(t *testing.T) {
	m, _ := fixture(t)
	failure := &failingTransport{}
	m.client = &http.Client{Transport: failure}
	m.RecordActivity(t.Context())
	m.sendPending(t.Context())
	m.sendPending(t.Context())
	if failure.calls.Load() != 1 {
		t.Fatal("failure retry not bounded")
	}
	m.nextAttempt = time.Time{}
	m.now = func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC) }
	m.sendPending(t.Context())
	if failure.calls.Load() != 1 {
		t.Fatal("stale day was replayed")
	}
}

func TestDisabledOrMissingRelayNeverSends(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		m, reports := fixture(t)
		if disabled {
			if err := m.deps.Store.SetSetting(t.Context(), EnabledSetting, "false"); err != nil {
				t.Fatal(err)
			}
		} else {
			m.deps.RelayURL = func() string { return "" }
		}
		m.RecordActivity(t.Context())
		m.sendPending(t.Context())
		if len(*reports) != 0 {
			t.Fatal(*reports)
		}
	}
}

func TestRunJoinsCancellation(t *testing.T) {
	m, _ := fixture(t)
	for _, candidate := range []*Module{m, NewModule(Deps{})} {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { candidate.Run(ctx); close(done) }()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("worker did not stop")
		}
	}
}

type blockedTransport struct{ started chan struct{} }

func (b blockedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	close(b.started)
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestOptOutCancelsInFlight(t *testing.T) {
	m, _ := fixture(t)
	started, done := make(chan struct{}), make(chan struct{})
	m.client = &http.Client{Transport: blockedTransport{started}}
	m.RecordActivity(t.Context())
	go func() { m.sendPending(t.Context()); close(done) }()
	<-started
	m.RecordActivity(t.Context()) // Local recording must not block on the export.
	if w := setting(t, m, "POST", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("opt-out did not cancel the export")
	}
	if pending, err := m.pending(t.Context(), m.now()); err != nil || pending != nil {
		t.Fatal(pending, err)
	}
}

func TestFileConfiguredOptOut(t *testing.T) {
	m, reports := fixture(t)
	if err := m.deps.Store.EnableFileConfig(t.Context(), filepath.Join(t.TempDir(), "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := m.deps.Store.SetSetting(t.Context(), EnabledSetting, "false"); err != nil {
		t.Fatal(err)
	}
	m.RecordActivity(t.Context())
	m.sendPending(t.Context())
	if len(*reports) != 0 {
		t.Fatal("file-configured opt-out ignored")
	}
	if w := setting(t, m, "GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
