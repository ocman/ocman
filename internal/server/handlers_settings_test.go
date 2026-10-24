package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	srv := &Server{stateDB: openTestStateDB(t)}

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
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	serveSetting(srv.worktreeInheritSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/worktree-inherit-permissions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}

func decodeAutoArchiveSettings(t *testing.T, body []byte) autoArchiveSettingsView {
	t.Helper()
	var settings autoArchiveSettingsView
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf("decoding response %q: %v", body, err)
	}
	return settings
}

func TestHandleAutoArchiveSettings(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}

	rec := httptest.NewRecorder()
	serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/auto-archive", nil))
	if got := decodeAutoArchiveSettings(t, rec.Body.Bytes()); rec.Code != http.StatusOK || !got.Enabled || got.TTLDays != 7 {
		t.Fatalf("GET default: status=%d settings=%+v, want 200/enabled/7 days", rec.Code, got)
	}

	rec = httptest.NewRecorder()
	serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/settings/auto-archive", strings.NewReader(`{"enabled":false,"ttlDays":30}`)))
	if got := decodeAutoArchiveSettings(t, rec.Body.Bytes()); rec.Code != http.StatusOK || got.Enabled || got.TTLDays != 30 {
		t.Fatalf("POST: status=%d settings=%+v, want 200/disabled/30 days", rec.Code, got)
	}

	rec = httptest.NewRecorder()
	serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/auto-archive", nil))
	if got := decodeAutoArchiveSettings(t, rec.Body.Bytes()); got.Enabled || got.TTLDays != 30 {
		t.Fatalf("GET saved settings = %+v, want disabled/30 days", got)
	}
}

func TestHandleAutoArchiveSettingsRejectsInvalidTTL(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	for _, ttl := range []int{0, 3651} {
		rec := httptest.NewRecorder()
		serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/settings/auto-archive", strings.NewReader(fmt.Sprintf(`{"enabled":true,"ttlDays":%d}`, ttl))))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("ttlDays=%d status=%d, want 400", ttl, rec.Code)
		}
	}
}

func TestHandleAutoArchiveSettingsRejectsInvalidRequest(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}

	rec := httptest.NewRecorder()
	serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/settings/auto-archive", strings.NewReader(`{bad`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/auto-archive", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status=%d, want 405", rec.Code)
	}
}

func TestHandleAutoArchiveSettingsUnavailable(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	serveSetting(srv.autoArchiveSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/auto-archive", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status=%d, want 503", rec.Code)
	}
}
