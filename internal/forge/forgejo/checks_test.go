package forgejo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func TestChecks_ParsesCombinedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/repos/o/r/commits/deadbeef/status"; got != want {
			t.Errorf("path: got %s want %s", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"state": "failure",
			"statuses": [
				{"status": "success", "context": "build", "target_url": "https://ci/build"},
				{"status": "failure", "context": "test", "target_url": "https://ci/test"}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "deadbeef")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStateFailure {
		t.Errorf("state: got %q want failure", ci.State)
	}
	if len(ci.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(ci.Checks))
	}
	if ci.Checks[0].Name != "build" || ci.Checks[0].State != forge.CIStateSuccess {
		t.Errorf("check 0 mapped wrong: %+v", ci.Checks[0])
	}
	if ci.Checks[1].State != forge.CIStateFailure {
		t.Errorf("check 1 mapped wrong: %+v", ci.Checks[1])
	}
}

func TestChecks_NoStatusesIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state": "", "statuses": []}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "deadbeef")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", ci.State)
	}
}

func TestChecks_NotFoundIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, _, err := c.Checks(context.Background(), "o/r", "deadbeef")
	if err != nil {
		t.Fatalf("Checks: %v", err)
	}
	if ci.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", ci.State)
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

func TestChecks_ServerErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, _, err := c.Checks(context.Background(), "o/r", "deadbeef"); err == nil {
		t.Fatalf("expected error on 500")
	}
}

func TestChecks_RateLimitedIsUnknownNotError(t *testing.T) {
	// A 429 from the combined-status endpoint must be swallowed
	// (CIStateUnknown, no error) with Limited surfaced on the
	// returned RateLimit.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	ci, rl, err := c.Checks(context.Background(), "o/r", "deadbeef")
	if err != nil {
		t.Fatalf("Checks: unexpected error on 429: %v", err)
	}
	if ci.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", ci.State)
	}
	if !rl.Limited {
		t.Errorf("expected RateLimit.Limited=true on 429")
	}
}

func TestChecks_MalformedJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"statuses": not-valid-json}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, "tok")
	if _, _, err := c.Checks(context.Background(), "o/r", "deadbeef"); err == nil {
		t.Fatalf("expected a decode error on malformed JSON")
	}
}

func TestFjStatusState(t *testing.T) {
	cases := map[string]forge.CIState{
		"success": forge.CIStateSuccess,
		"warning": forge.CIStateSuccess,
		"pending": forge.CIStatePending,
		"running": forge.CIStatePending,
		"failure": forge.CIStateFailure,
		"error":   forge.CIStateFailure,
		"nope":    forge.CIStateUnknown,
		"":        forge.CIStateUnknown,
	}
	for in, want := range cases {
		if got := fjStatusState(in); got != want {
			t.Errorf("fjStatusState(%q) = %q, want %q", in, got, want)
		}
	}
}
