package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// checksServer routes the two endpoints Checks() hits to the supplied
// JSON bodies. A 404 body is sent when the corresponding string is
// empty, mirroring "no statuses / no check runs".
func checksServer(t *testing.T, statusBody, checkRunsBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/commits/abc123/status":
			if statusBody == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(statusBody))
		case "/repos/o/r/commits/abc123/check-runs":
			if checkRunsBody == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(checkRunsBody))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestChecks_CombinesStatusesAndCheckRuns(t *testing.T) {
	srv := checksServer(t,
		`{"statuses":[{"state":"success","context":"ci/lint","target_url":"https://ci/lint"}]}`,
		`{"check_runs":[{"name":"build","status":"completed","conclusion":"success","html_url":"https://gh/build"},{"name":"test","status":"in_progress","conclusion":null,"html_url":"https://gh/test"}]}`,
	)
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "abc123")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStatePending {
		t.Errorf("state: got %q want pending (one run in progress)", ci.State)
	}
	if len(ci.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(ci.Checks))
	}
	if ci.Checks[0].Name != "ci/lint" || ci.Checks[0].State != forge.CIStateSuccess {
		t.Errorf("status check mapped wrong: %+v", ci.Checks[0])
	}
}

func TestChecks_FailureRollsUp(t *testing.T) {
	srv := checksServer(t,
		"",
		`{"check_runs":[{"name":"build","status":"completed","conclusion":"success"},{"name":"test","status":"completed","conclusion":"failure"}]}`,
	)
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "abc123")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStateFailure {
		t.Errorf("state: got %q want failure", ci.State)
	}
}

func TestChecks_NoCIIsUnknown(t *testing.T) {
	srv := checksServer(t,
		`{"statuses":[]}`,
		`{"check_runs":[]}`,
	)
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "abc123")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", ci.State)
	}
	if len(ci.Checks) != 0 {
		t.Errorf("expected no checks, got %d", len(ci.Checks))
	}
}

func TestChecks_EmptySHASkipsFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Checks should not call the API for an empty SHA (hit %s)", r.URL.Path)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", ci.State)
	}
}

func TestChecks_StatusEndpointErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, _, err := c.Checks(context.Background(), "o/r", "abc123"); err == nil {
		t.Fatalf("expected error on 500 from status endpoint")
	}
}

func TestChecks_CheckRunsEndpointErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// status OK (empty), check-runs errors.
		if r.URL.Path == "/repos/o/r/commits/abc123/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"statuses":[]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, _, err := c.Checks(context.Background(), "o/r", "abc123"); err == nil {
		t.Fatalf("expected error on 500 from check-runs endpoint")
	}
}

func TestChecks_RateLimitedEndpointsAreUnknownNotError(t *testing.T) {
	// Both endpoints answer 429. Checks() must swallow the rate limit
	// (returning CIStateUnknown with no checks) rather than erroring,
	// and surface Limited=true via the returned RateLimit.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, rl, err := c.Checks(context.Background(), "o/r", "abc123")
	if err != nil {
		t.Fatalf("Checks: unexpected error on 429: %v", err)
	}
	if ci.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", ci.State)
	}
	if len(ci.Checks) != 0 {
		t.Errorf("expected no checks on 429, got %d", len(ci.Checks))
	}
	if !rl.Limited {
		t.Errorf("expected RateLimit.Limited=true on 429")
	}
}

func TestChecks_CheckRunsRateLimitPreferredOverStatus(t *testing.T) {
	// status endpoint OK (not limited), check-runs endpoint 429. The
	// rolled-up RateLimit should reflect the check-runs reading
	// (Limited=true) per the rl2-preference branch in Checks().
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/commits/abc123/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"statuses":[{"state":"success","context":"ci/lint"}]}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, rl, err := c.Checks(context.Background(), "o/r", "abc123")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if !rl.Limited {
		t.Errorf("expected RateLimit.Limited=true from the check-runs endpoint")
	}
	// The single successful status still rolls up to success.
	if ci.State != forge.CIStateSuccess {
		t.Errorf("state: got %q want success", ci.State)
	}
}

func TestChecks_MalformedStatusJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Status endpoint returns invalid JSON.
		_, _ = w.Write([]byte(`{"statuses": not-json}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, _, err := c.Checks(context.Background(), "o/r", "abc123"); err == nil {
		t.Fatalf("expected a decode error on malformed status JSON")
	}
}

func TestChecks_MalformedCheckRunsJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/o/r/commits/abc123/status" {
			_, _ = w.Write([]byte(`{"statuses":[]}`))
			return
		}
		// check-runs endpoint returns invalid JSON.
		_, _ = w.Write([]byte(`{"check_runs": <broken>}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, _, err := c.Checks(context.Background(), "o/r", "abc123"); err == nil {
		t.Fatalf("expected a decode error on malformed check-runs JSON")
	}
}

func TestGhStatusState(t *testing.T) {
	cases := map[string]forge.CIState{
		"success": forge.CIStateSuccess,
		"pending": forge.CIStatePending,
		"failure": forge.CIStateFailure,
		"error":   forge.CIStateFailure,
		"weird":   forge.CIStateUnknown,
		"":        forge.CIStateUnknown,
	}
	for in, want := range cases {
		if got := ghStatusState(in); got != want {
			t.Errorf("ghStatusState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGhCheckRunState(t *testing.T) {
	type in struct{ status, conclusion string }
	cases := map[in]forge.CIState{
		{"queued", ""}:                  forge.CIStatePending,
		{"in_progress", ""}:             forge.CIStatePending,
		{"completed", "success"}:        forge.CIStateSuccess,
		{"completed", "neutral"}:        forge.CIStateSuccess,
		{"completed", "skipped"}:        forge.CIStateSuccess,
		{"completed", "failure"}:        forge.CIStateFailure,
		{"completed", "timed_out"}:      forge.CIStateFailure,
		{"completed", "cancelled"}:      forge.CIStateFailure,
		{"completed", "action_required"}: forge.CIStateFailure,
		{"completed", "stale"}:          forge.CIStateFailure,
		{"completed", "mystery"}:        forge.CIStateUnknown,
	}
	for k, want := range cases {
		if got := ghCheckRunState(k.status, k.conclusion); got != want {
			t.Errorf("ghCheckRunState(%q,%q) = %q, want %q", k.status, k.conclusion, got, want)
		}
	}
}
