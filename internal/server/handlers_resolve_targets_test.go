package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/remote"
)

func TestHandleResolveTargets_SingleHostLocalOnly(t *testing.T) {
	srv := testServer(t) // no remote manager

	body := `{"dir":"/some/project"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Candidates []remote.TargetCandidate `json:"candidates"`
		Remotes    []remote.TargetCandidate `json:"remotes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].RemoteID != "local" {
		t.Fatalf("expected single local candidate, got %+v", resp.Candidates)
	}
	if resp.Candidates[0].Dir != "/some/project" {
		t.Fatalf("candidate dir = %q", resp.Candidates[0].Dir)
	}
}

func TestHandleResolveTargets_RequiresDir(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
