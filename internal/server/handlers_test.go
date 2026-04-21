package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// --- validateID tests ---

func TestValidateID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"valid simple", "abc123", true},
		{"valid with hyphens", "abc-def-123", true},
		{"valid with underscores", "abc_def_123", true},
		{"valid mixed", "a1-b2_c3", true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", 257), false},
		{"max length", strings.Repeat("a", 256), true},
		{"with spaces", "abc def", false},
		{"with slash", "abc/def", false},
		{"with dots", "abc.def", false},
		{"special chars", "abc!@#", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateID(tt.id)
			if got != tt.want {
				t.Errorf("validateID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// parseOpenCodeModelRef tests moved to internal/platforms/opencode/
// together with the function itself.

// --- requireGET / requirePOST tests ---

func TestRequireGET(t *testing.T) {
	handler := requireGET(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// GET should pass through
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("GET: expected 200, got %d", rr.Code)
	}

	// POST should be rejected
	req = httptest.NewRequest("POST", "/test", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: expected 405, got %d", rr.Code)
	}
}

func TestRequirePOST(t *testing.T) {
	handler := requirePOST(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// POST should pass through
	req := httptest.NewRequest("POST", "/test", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("POST: expected 200, got %d", rr.Code)
	}

	// GET should be rejected
	req = httptest.NewRequest("GET", "/test", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: expected 405, got %d", rr.Code)
	}
}

// --- requireLocalhost tests ---

func TestRequireLocalhost(t *testing.T) {
	handler := requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		remoteAddr string
		wantCode   int
	}{
		{"IPv4 loopback", "127.0.0.1:12345", http.StatusOK},
		{"IPv6 loopback", "[::1]:12345", http.StatusOK},
		{"external IP", "192.168.1.100:12345", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("RemoteAddr=%q: expected %d, got %d", tt.remoteAddr, tt.wantCode, rr.Code)
			}
		})
	}
}

// --- isLoopback tests ---

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		remoteAddr string
		want       bool
	}{
		{"127.0.0.1:8229", true},
		{"[::1]:8229", true},
		{"192.168.1.1:8229", false},
		{"10.0.0.1:443", false},
	}
	for _, tt := range tests {
		t.Run(tt.remoteAddr, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr}
			if got := isLoopback(r); got != tt.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

// --- readAndUnmarshal tests ---

func TestReadAndUnmarshal_ValidJSON(t *testing.T) {
	body := strings.NewReader(`{"name":"test","value":42}`)
	req := httptest.NewRequest("POST", "/test", body)
	rr := httptest.NewRecorder()

	var dst struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	ok := readAndUnmarshal(rr, req, 1024, &dst)
	if !ok {
		t.Fatal("expected true for valid JSON")
	}
	if dst.Name != "test" || dst.Value != 42 {
		t.Errorf("unexpected parsed values: %+v", dst)
	}
}

func TestReadAndUnmarshal_InvalidJSON(t *testing.T) {
	body := strings.NewReader(`{invalid}`)
	req := httptest.NewRequest("POST", "/test", body)
	rr := httptest.NewRecorder()

	var dst struct{}
	ok := readAndUnmarshal(rr, req, 1024, &dst)
	if ok {
		t.Fatal("expected false for invalid JSON")
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- writeJSON tests ---

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, map[string]string{"key": "value"})

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"key":"value"`) {
		t.Errorf("unexpected body: %s", body)
	}
}

// --- writePlatformError tests ---

// TestWritePlatformError_MapsErrBusyTo409 ensures AD-13's wire
// contract: a SendMessage that returns platforms.ErrBusy must surface
// as HTTP 409 Conflict so the frontend can show a distinct "try
// again" toast rather than the generic "upstream failed" banner used
// for 502s.
func TestWritePlatformError_MapsErrBusyTo409(t *testing.T) {
	rr := httptest.NewRecorder()
	writePlatformError(rr, "sending message", platforms.ErrBusy)
	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusConflict)
	}
	if !strings.Contains(rr.Body.String(), "processing") {
		t.Errorf("body missing explanation, got %q", rr.Body.String())
	}
}

// TestWritePlatformError_MapsErrUnsupportedTo501 is a regression test
// for the existing sentinel mapping — added alongside ErrBusy so a
// future edit can't silently break either route.
func TestWritePlatformError_MapsErrUnsupportedTo501(t *testing.T) {
	rr := httptest.NewRecorder()
	writePlatformError(rr, "sending message", platforms.ErrUnsupported)
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotImplemented)
	}
}

// TestWritePlatformError_MapsUnknownTo502 covers the default branch.
func TestWritePlatformError_MapsUnknownTo502(t *testing.T) {
	rr := httptest.NewRecorder()
	writePlatformError(rr, "sending message", errors.New("unexpected"))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
}
