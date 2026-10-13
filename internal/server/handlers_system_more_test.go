package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestSystemHandlers_RequireDB(t *testing.T) {
	srv := testServer(t)
	srv.db = nil // simulate ocman running without the opencode platform

	handlers := map[string]http.HandlerFunc{
		"stats":         srv.handleStats,
		"metrics":       srv.handleMetrics,
		"activity":      srv.handleActivity,
		"models":        srv.handleModels,
		"hourly":        srv.handleHourly,
		"hourly-tokens": srv.handleHourlyTokens,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/"+name, nil)
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != http.StatusNotImplemented {
				t.Fatalf("got %d, want 501: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// The usage views pass model/dir/day filters straight through; assert
// they stay 200 with an empty database rather than pinning SQL shapes.
func TestUsageHandlers_FilterParams(t *testing.T) {
	srv := testServer(t)
	handlers := map[string]http.HandlerFunc{
		"activity":      srv.handleActivity,
		"models":        srv.handleModels,
		"hourly":        srv.handleHourly,
		"hourly-tokens": srv.handleHourlyTokens,
		"metrics":       srv.handleMetrics,
	}
	queries := []string{
		"",
		"?days=0",
		"?days=7&model=anthropic/claude",
		"?since=1700000000000&dir=" + url.QueryEscape("/tmp/project"),
		"?limit=abc&offset=abc&sessionLimit=5&sessionOffset=1&projectLimit=5&projectOffset=1",
	}
	for name, h := range handlers {
		for _, q := range queries {
			t.Run(name+q, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/api/"+name+q, nil)
				rr := httptest.NewRecorder()
				h(rr, req)
				if rr.Code != http.StatusOK {
					t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
				}
			})
		}
	}
}

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

func TestHandleCalcCost(t *testing.T) {
	srv := testServer(t)
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"malformed json", "{", http.StatusBadRequest},
		{"empty body", "", http.StatusBadRequest},
		{"wrong types", `{"input":"lots"}`, http.StatusBadRequest},
		{"unknown model", `{"modelID":"nope/nope","input":10,"output":5}`, http.StatusOK},
		{"zero usage", `{"modelID":"","input":0,"output":0}`, http.StatusOK},
		{"with cache tokens", `{"modelID":"anthropic/claude-sonnet-4-20250514","input":100,"output":50,"cacheRead":10,"cacheWrite":20}`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/calc-cost", strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			srv.handleCalcCost(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			var payload struct {
				Cost  float64 `json:"cost"`
				Known bool    `json:"known"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatalf("invalid JSON: %v (%s)", err, rr.Body.String())
			}
			if payload.Cost < 0 {
				t.Errorf("cost = %v, want >= 0", payload.Cost)
			}
			if strings.Contains(tt.name, "unknown") && payload.Known {
				t.Error("known = true for an unknown model")
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
	h := requirePOST(srv.requireLocalhost(srv.handleDebugLog))
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
			h(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}
