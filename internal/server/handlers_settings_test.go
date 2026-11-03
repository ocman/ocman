package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state/statetest"
)

func decodeEnabled(t *testing.T, body []byte) bool {
	t.Helper()
	var resp struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding response %q: %v", body, err)
	}
	return resp.Enabled
}

func TestHandleWorktreeInheritPermissions(t *testing.T) {
	srv := &Server{stateDB: statetest.Open(t, ModuleSchemas()...)}

	// Default (unset) → enabled true.
	rec := httptest.NewRecorder()
	serveSetting(srv.worktreeInheritSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/worktree-inherit-permissions", nil))
	if rec.Code != http.StatusOK || !decodeEnabled(t, rec.Body.Bytes()) {
		t.Fatalf("GET default: status=%d enabled=%v, want 200/true", rec.Code, decodeEnabled(t, rec.Body.Bytes()))
	}

	// POST disable, then GET returns false.
	rec = httptest.NewRecorder()
	serveSetting(srv.worktreeInheritSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/settings/worktree-inherit-permissions", strings.NewReader(`{"enabled":false}`)))
	if rec.Code != http.StatusOK || decodeEnabled(t, rec.Body.Bytes()) {
		t.Fatalf("POST disable: status=%d, want 200/false", rec.Code)
	}
	rec = httptest.NewRecorder()
	serveSetting(srv.worktreeInheritSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/worktree-inherit-permissions", nil))
	if decodeEnabled(t, rec.Body.Bytes()) {
		t.Fatalf("GET after disable = true, want false")
	}
}

func TestHandleWorktreeInheritPermissionsMethodNotAllowed(t *testing.T) {
	srv := &Server{stateDB: statetest.Open(t, ModuleSchemas()...)}
	rec := httptest.NewRecorder()
	serveSetting(srv.worktreeInheritSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/worktree-inherit-permissions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}
