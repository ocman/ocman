package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
)

func decodeModel(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding response %q: %v", body, err)
	}
	return resp.Model
}

func TestHandleGetJudgeModel(t *testing.T) {
	sdb := openTestStateDB(t)
	srv := &Server{stateDB: sdb}

	// Unset → empty string.
	rec := httptest.NewRecorder()
	srv.handleJudgeModel(rec, httptest.NewRequest(http.MethodGet, "/api/settings/judge-model", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET unset status = %d, want 200", rec.Code)
	}
	if got := decodeModel(t, rec.Body.Bytes()); got != "" {
		t.Fatalf("GET unset model = %q, want empty", got)
	}

	// After a set, GET returns the stored value.
	if err := sdb.SetSetting(t.Context(), autoapprove.JudgeModelSettingKey, "anthropic/claude-haiku-4-5"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.handleJudgeModel(rec, httptest.NewRequest(http.MethodGet, "/api/settings/judge-model", nil))
	if got := decodeModel(t, rec.Body.Bytes()); got != "anthropic/claude-haiku-4-5" {
		t.Fatalf("GET set model = %q, want anthropic/claude-haiku-4-5", got)
	}
}

func TestHandleSetJudgeModel(t *testing.T) {
	sdb := openTestStateDB(t)
	srv := &Server{stateDB: sdb}

	// Valid value is persisted and applied to the running judge.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/judge-model",
		strings.NewReader(`{"model":"openrouter/anthropic/claude"}`))
	srv.handleJudgeModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	if got := decodeModel(t, rec.Body.Bytes()); got != "openrouter/anthropic/claude" {
		t.Fatalf("POST echoed model = %q", got)
	}
	if provider, modelID := srv.aaSvc().JudgeModel(); provider != "openrouter" || modelID != "anthropic/claude" {
		t.Fatalf("judge not updated: provider=%q model=%q", provider, modelID)
	}
	if v, ok, _ := sdb.GetSetting(t.Context(), autoapprove.JudgeModelSettingKey); !ok || v != "openrouter/anthropic/claude" {
		t.Fatalf("setting not persisted: %q ok=%v", v, ok)
	}

	// Empty value clears the setting and reverts the judge to defaults
	// (the built-in anthropic haiku model).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings/judge-model",
		strings.NewReader(`{"model":""}`))
	srv.handleJudgeModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST clear status = %d, want 200", rec.Code)
	}
	if provider, modelID := srv.aaSvc().JudgeModel(); provider != "anthropic" || modelID != "claude-haiku-4-5" {
		t.Fatalf("judge not reverted to defaults: provider=%q model=%q", provider, modelID)
	}
}

func TestHandleSetJudgeModelInvalidJSON(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/judge-model", strings.NewReader(`{bad`))
	srv.handleJudgeModel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", rec.Code)
	}
}

func TestHandleSetJudgeModelNoStateDB(t *testing.T) {
	srv := &Server{} // no stateDB
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/judge-model",
		strings.NewReader(`{"model":"a/b"}`))
	srv.handleJudgeModel(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no stateDB status = %d, want 503", rec.Code)
	}
}

func TestHandleJudgeModelMethodNotAllowed(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	srv.handleJudgeModel(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/judge-model", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}

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
	srv.handleWorktreeInheritPermissions(rec, httptest.NewRequest(http.MethodGet, "/api/settings/worktree-inherit-permissions", nil))
	if rec.Code != http.StatusOK || !decodeEnabled(t, rec.Body.Bytes()) {
		t.Fatalf("GET default: status=%d enabled=%v, want 200/true", rec.Code, decodeEnabled(t, rec.Body.Bytes()))
	}

	// POST disable, then GET returns false.
	rec = httptest.NewRecorder()
	srv.handleWorktreeInheritPermissions(rec, httptest.NewRequest(http.MethodPost,
		"/api/settings/worktree-inherit-permissions", strings.NewReader(`{"enabled":false}`)))
	if rec.Code != http.StatusOK || decodeEnabled(t, rec.Body.Bytes()) {
		t.Fatalf("POST disable: status=%d, want 200/false", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.handleWorktreeInheritPermissions(rec, httptest.NewRequest(http.MethodGet, "/api/settings/worktree-inherit-permissions", nil))
	if decodeEnabled(t, rec.Body.Bytes()) {
		t.Fatalf("GET after disable = true, want false")
	}
}

func TestHandleWorktreeInheritPermissionsMethodNotAllowed(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	srv.handleWorktreeInheritPermissions(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/worktree-inherit-permissions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}
