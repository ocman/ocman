package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSettingStore implements judgeModelStore for loadJudgeModel tests.
type fakeSettingStore struct {
	val string
	ok  bool
	err error
}

func (f fakeSettingStore) GetSetting(string) (string, bool, error) {
	return f.val, f.ok, f.err
}

func TestLoadJudgeModel(t *testing.T) {
	tests := []struct {
		name         string
		store        judgeModelStore
		wantProvider string
		wantModel    string
		wantOK       bool
	}{
		{"nil store", nil, "", "", false},
		{"unset", fakeSettingStore{ok: false}, "", "", false},
		{"error", fakeSettingStore{val: "a/b", ok: true, err: errors.New("x")}, "", "", false},
		{"valid", fakeSettingStore{val: "anthropic/claude-haiku-4-5", ok: true}, "anthropic", "claude-haiku-4-5", true},
		{"model with slash", fakeSettingStore{val: "openrouter/anthropic/claude", ok: true}, "openrouter", "anthropic/claude", true},
		{"no slash", fakeSettingStore{val: "bogus", ok: true}, "", "", false},
		{"leading slash", fakeSettingStore{val: "/model", ok: true}, "", "", false},
		{"trailing slash", fakeSettingStore{val: "provider/", ok: true}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model, ok := loadJudgeModel(tt.store)
			if ok != tt.wantOK || provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("loadJudgeModel = (%q, %q, %v), want (%q, %q, %v)",
					provider, model, ok, tt.wantProvider, tt.wantModel, tt.wantOK)
			}
		})
	}
}

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
	sdb := openWatcherTestStateDB(t)
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
	if err := sdb.SetSetting(judgeModelSettingKey, "anthropic/claude-haiku-4-5"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.handleJudgeModel(rec, httptest.NewRequest(http.MethodGet, "/api/settings/judge-model", nil))
	if got := decodeModel(t, rec.Body.Bytes()); got != "anthropic/claude-haiku-4-5" {
		t.Fatalf("GET set model = %q, want anthropic/claude-haiku-4-5", got)
	}
}

func TestHandleSetJudgeModel(t *testing.T) {
	sdb := openWatcherTestStateDB(t)
	judge := newPermissionJudge()
	srv := &Server{stateDB: sdb, judge: judge}

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
	if judge.modelProvider != "openrouter" || judge.modelID != "anthropic/claude" {
		t.Fatalf("judge not updated: provider=%q model=%q", judge.modelProvider, judge.modelID)
	}
	if v, ok, _ := sdb.GetSetting(judgeModelSettingKey); !ok || v != "openrouter/anthropic/claude" {
		t.Fatalf("setting not persisted: %q ok=%v", v, ok)
	}

	// Empty value clears the setting and reverts the judge to defaults.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings/judge-model",
		strings.NewReader(`{"model":""}`))
	srv.handleJudgeModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST clear status = %d, want 200", rec.Code)
	}
	if judge.modelProvider != judgeModelProvider || judge.modelID != judgeModelID {
		t.Fatalf("judge not reverted to defaults: provider=%q model=%q", judge.modelProvider, judge.modelID)
	}
}

func TestHandleSetJudgeModelInvalidJSON(t *testing.T) {
	srv := &Server{stateDB: openWatcherTestStateDB(t)}
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
	srv := &Server{stateDB: openWatcherTestStateDB(t)}
	rec := httptest.NewRecorder()
	srv.handleJudgeModel(rec, httptest.NewRequest(http.MethodDelete, "/api/settings/judge-model", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}
