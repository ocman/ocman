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
