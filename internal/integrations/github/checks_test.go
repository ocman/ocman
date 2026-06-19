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
		switch {
		case r.URL.Path == "/repos/o/r/commits/abc123/status":
			if statusBody == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(statusBody))
		case r.URL.Path == "/repos/o/r/commits/abc123/check-runs":
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
