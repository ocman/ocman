package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/whisper"
)

func getJSON[T any](t *testing.T, h http.HandlerFunc, target string) T {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: got %d, want 200: %s", target, rr.Code, rr.Body.String())
	}
	var out T
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET %s: invalid JSON: %v (%s)", target, err, rr.Body.String())
	}
	return out
}

// --- handlers_system.go ---

func TestHandleWhisperStatus(t *testing.T) {
	srv := testServer(t)
	got := getJSON[map[string]any](t, srv.handleWhisperStatus, "/api/whisper-status")
	available, ok := got["available"].(bool)
	if !ok {
		t.Fatalf("payload = %v, want an \"available\" bool", got)
	}
	if available != whisper.Available() {
		t.Errorf("available = %v, want %v", available, whisper.Available())
	}
}

// Without whisper installed the endpoint reports 503; with it installed
// a bodyless POST fails multipart parsing with 400. Either way the
// handler must never 200 on a request carrying no audio.
func TestHandleTranscribe_NoAudio(t *testing.T) {
	srv := testServer(t)
	want := http.StatusServiceUnavailable
	if whisper.Available() {
		want = http.StatusBadRequest
	}
	for _, tt := range []struct {
		name        string
		contentType string
	}{
		{"no body", ""},
		{"multipart without audio field", "multipart/form-data; boundary=zz"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/transcribe", strings.NewReader(""))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rr := httptest.NewRecorder()
			srv.handleTranscribe(rr, req)
			if rr.Code != want {
				t.Fatalf("got %d, want %d: %s", rr.Code, want, rr.Body.String())
			}
		})
	}
}

func TestHandleDebugLog(t *testing.T) {
	srv := testServer(t)
	tests := []struct {
		name      string
		body      string
		userAgent string
		wantCode  int
	}{
		{"malformed json", "{", "", http.StatusBadRequest},
		{"error level", `{"level":"error","message":"boom"}`, "", http.StatusNoContent},
		{"warn level", `{"level":"warn","message":"careful"}`, "", http.StatusNoContent},
		{"warning alias", `{"level":"WARNING","message":"careful"}`, "", http.StatusNoContent},
		{"debug level", `{"level":" Debug ","message":"noisy"}`, "", http.StatusNoContent},
		{"unknown level falls back to info", `{"level":"trace","message":"hm"}`, "", http.StatusNoContent},
		{"empty level", `{"message":"hm"}`, "", http.StatusNoContent},
		{"with data and user agent", `{"level":"info","message":"hm","data":{"a":1}}`, "ocman-test/1.0", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/debug/log", strings.NewReader(tt.body))
			if tt.userAgent != "" {
				req.Header.Set("User-Agent", tt.userAgent)
			}
			rr := httptest.NewRecorder()
			srv.handleDebugLog(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}

// /api/debug/log is POST-only and localhost-only.
func TestDebugLogRoute_MethodAndLocalhostEnforcement(t *testing.T) {
	srv := testServer(t)
	tests := []struct {
		name       string
		method     string
		remoteAddr string
		origin     string
		wantCode   int
	}{
		{"post from loopback", http.MethodPost, "127.0.0.1:1234", "", http.StatusNoContent},
		{"get rejected", http.MethodGet, "127.0.0.1:1234", "", http.StatusMethodNotAllowed},
		{"remote peer rejected", http.MethodPost, "10.1.2.3:1234", "", http.StatusForbidden},
		{"foreign origin rejected", http.MethodPost, "127.0.0.1:1234", "https://evil.example", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/debug/log", strings.NewReader(`{"message":"hi"}`))
			req.RemoteAddr = tt.remoteAddr
			req.Host = "localhost:8228"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rr := httptest.NewRecorder()
			serveRoutes(t, srv, rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}
