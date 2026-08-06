package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// cleanGitEnvForTest returns os.Environ() with git context variables
// stripped. Pre-commit hooks inject GIT_DIR, GIT_INDEX_FILE, etc. which
// would redirect git subprocesses into the wrong repository.
func cleanGitEnvForTest() []string {
	return gitexec.CleanEnv()
}

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
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("GET: expected 200, got %d", rr.Code)
	}

	// POST should be rejected
	req = httptest.NewRequest(http.MethodPost, "/test", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("POST: expected 200, got %d", rr.Code)
	}

	// GET should be rejected
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: expected 405, got %d", rr.Code)
	}
}

// --- requireLocalhost tests ---

func TestRequireLocalhost(t *testing.T) {
	handler := (&Server{}).requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		remoteAddr string
		host       string
		origin     string
		fetchSite  string
		wantCode   int
	}{
		{"IPv4 native", "127.0.0.1:12345", "localhost:8228", "", "", http.StatusOK},
		{"IPv6 native", "[::1]:12345", "[::1]:8228", "", "", http.StatusOK},
		{"external IP", "192.168.1.100:12345", "localhost:8228", "", "", http.StatusForbidden},
		{"same origin browser", "127.0.0.1:12345", "localhost:8228", "http://localhost:8228", "same-origin", http.StatusOK},
		{"foreign origin", "127.0.0.1:12345", "localhost:8228", "https://evil.example", "cross-site", http.StatusForbidden},
		{"null origin", "127.0.0.1:12345", "localhost:8228", "null", "", http.StatusForbidden},
		{"scheme mismatch", "127.0.0.1:12345", "localhost:8228", "https://localhost:8228", "", http.StatusForbidden},
		{"port mismatch", "127.0.0.1:12345", "localhost:8228", "http://localhost:9999", "", http.StatusForbidden},
		{"fetch metadata only", "127.0.0.1:12345", "localhost:8228", "", "cross-site", http.StatusForbidden},
		{"DNS rebinding host", "127.0.0.1:12345", "evil.example", "http://evil.example", "same-origin", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("RemoteAddr=%q: expected %d, got %d", tt.remoteAddr, tt.wantCode, rr.Code)
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "localhost:8228"
	req.Header.Add("Origin", "http://localhost:8228")
	req.Header.Add("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("multiple origins: expected 403, got %d", rr.Code)
	}
}

func TestRequireLocalhost_AllowsAuthenticatedPublicOrigin(t *testing.T) {
	auth := newTestAuth(t, "hunter2", withTrustLocalhost())
	srv := &Server{auth: auth, publicBaseURL: "https://ocman.example.com"}
	handler := srv.requireLocalhost(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "http://ocman.example.com/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "ocman.example.com"
	req.Header.Set("Origin", "https://ocman.example.com")
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: auth.signToken(time.Now().Add(time.Hour))})
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("authenticated public origin: got %d, want 204", rr.Code)
	}
}

// requireLoopbackPeer backs /mcp: native MCP clients can't send an auth
// cookie, so a loopback peer is accepted even with a password configured.
func TestRequireLoopbackPeer(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "hunter2")}
	handler := srv.requireLoopbackPeer(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name       string
		remoteAddr string
		origin     string
		fetchSite  string
		wantCode   int
	}{
		{"origin-less loopback", "127.0.0.1:12345", "", "", http.StatusNoContent},
		{"IPv6 loopback", "[::1]:12345", "", "", http.StatusNoContent},
		{"same origin browser", "127.0.0.1:12345", "http://localhost:8228", "same-origin", http.StatusNoContent},
		{"external IP", "192.0.2.1:12345", "", "", http.StatusForbidden},
		{"foreign origin", "127.0.0.1:12345", "https://evil.example", "cross-site", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Host = "localhost:8228"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("want %d, got %d", tt.wantCode, rr.Code)
			}
		})
	}
}

// --- s.post CSRF guard tests (#410) ---

// A cross-site POST must be rejected even when auth is disabled;
// Origin-less local CLI clients must keep working.
func TestPostCSRFGuard_AuthDisabled(t *testing.T) {
	srv := &Server{}
	handler := srv.post(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name      string
		origin    string
		fetchSite string
		wantCode  int
	}{
		{"no origin (CLI)", "", "", http.StatusNoContent},
		{"same origin", "http://localhost:8228", "same-origin", http.StatusNoContent},
		{"cross-site origin", "https://evil.example", "cross-site", http.StatusForbidden},
		{"mismatched origin only", "http://localhost:9999", "", http.StatusForbidden},
		{"fetch metadata only", "", "cross-site", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			req.Host = "localhost:8228"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("got %d, want %d", rr.Code, tt.wantCode)
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
	req := httptest.NewRequest(http.MethodPost, "/test", body)
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
	req := httptest.NewRequest(http.MethodPost, "/test", body)
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

func TestWritePlatformError_MapsAuthenticationWithoutSecret(t *testing.T) {
	const secret = "do-not-expose"
	rr := httptest.NewRecorder()
	err := fmt.Errorf("request failed: %w", ocapi.ErrAuthentication)
	writePlatformError(rr, "sending message", err)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rr.Body.String(), "authentication failed") {
		t.Fatalf("body missing auth diagnostic: %q", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), secret) {
		t.Fatalf("body exposed secret: %q", rr.Body.String())
	}
}

// TestWritePlatformError_MapsErrUpstreamRejectedTo422 covers the
// "platform was reached but rejected the request" path that surfaces
// errors like OpenCode's `ProviderModelNotFoundError` to the user.
// The upstream-supplied human message MUST land in the response body
// so the UI can render it instead of the generic "failed to reach
// platform instance" banner used for true 502s.
func TestWritePlatformError_MapsErrUpstreamRejectedTo422(t *testing.T) {
	t.Run("with message", func(t *testing.T) {
		rr := httptest.NewRecorder()
		ue := &platforms.UpstreamError{Status: 400, Message: "Model anthropic/foo not found"}
		writePlatformError(rr, "sending message", fmt.Errorf("opencode /x: %w", ue))
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		if !strings.Contains(rr.Body.String(), "Model anthropic/foo not found") {
			t.Errorf("body missing upstream message, got %q", rr.Body.String())
		}
	})
	t.Run("empty message falls back to generic body", func(t *testing.T) {
		rr := httptest.NewRecorder()
		ue := &platforms.UpstreamError{Status: 400}
		writePlatformError(rr, "sending message", ue)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		if !strings.Contains(rr.Body.String(), "rejected") {
			t.Errorf("body missing fallback, got %q", rr.Body.String())
		}
	})
}

// TestWritePlatformError_MapsErrPlatformUnreachableTo503 ensures the
// "no running platform instance" case surfaces as 503 Service
// Unavailable, which the frontend uses as the trigger to launch
// opencode in a new tmux window and retry. A wrapped error must also
// be recognised via errors.Is.
func TestWritePlatformError_MapsErrPlatformUnreachableTo503(t *testing.T) {
	t.Run("sentinel", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writePlatformError(rr, "creating session", platforms.ErrPlatformUnreachable)
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
		}
	})
	t.Run("wrapped", func(t *testing.T) {
		rr := httptest.NewRecorder()
		wrapped := fmt.Errorf("no running OpenCode instance for /tmp: %w", platforms.ErrPlatformUnreachable)
		writePlatformError(rr, "creating session", wrapped)
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("wrapped status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
		}
	})
}

func TestSystemStats(t *testing.T) {
	srv := New(nil, nil, "127.0.0.1:8229", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/system/stats", nil)
	rr := httptest.NewRecorder()

	srv.handleSystemStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify structure
	memory, ok := stats["memory"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'memory' field")
	}
	if _, ok := memory["heapAlloc"].(float64); !ok {
		t.Errorf("missing or invalid 'memory.heapAlloc'")
	}

	gc, ok := stats["gc"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'gc' field")
	}
	if _, ok := gc["numGC"].(float64); !ok {
		t.Errorf("missing or invalid 'gc.numGC'")
	}

	if _, ok := stats["goroutines"].(float64); !ok {
		t.Errorf("missing or invalid 'goroutines'")
	}

	uptime, ok := stats["uptime"].(float64)
	if !ok {
		t.Errorf("missing or invalid 'uptime'")
	}
	// Uptime should be very small (just started)
	if uptime < 0 || uptime > 1 {
		t.Errorf("uptime = %v, expected small positive value", uptime)
	}

	// With db=nil (this test's setup), the response must NOT include
	// a `db` block — the canary diagnostic only makes sense when a
	// connection pool is actually in use. The frontend can rely on
	// `db` being absent to mean "no opencode adapter registered".
	if _, has := stats["db"]; has {
		t.Errorf("db block present despite nil db; got %v", stats["db"])
	}
}

// TestSystemStats_IncludesDBPoolWhenDBPresent verifies that the
// handler surfaces the read-only DB connection-pool stats when the
// adapter is actually registered. These fields drive the diagnostic
// pattern documented in docs/other/profiling.md: if `wait_count` ever
// climbs, ocman is throttling its own queries on the pool cap and we
// need to either bump the cap or reduce concurrency.
func TestSystemStats_IncludesDBPoolWhenDBPresent(t *testing.T) {
	// Seed a minimal DB on disk so db.Open() succeeds — the same
	// helper the db package's pool tests use.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	rw, err := sql.Open("sqlite", "file:"+path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	_, err = rw.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`)
	if err != nil {
		_ = rw.Close()
		t.Fatalf("seed schema: %v", err)
	}
	_ = rw.Close()

	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer d.Close()

	srv := New(d, nil, "127.0.0.1:8229", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/system/stats", nil)
	rr := httptest.NewRecorder()
	srv.handleSystemStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var stats map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("parse: %v", err)
	}

	dbBlock, ok := stats["db"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing or invalid 'db' field; full response: %v", stats)
	}
	for _, key := range []string{
		"max_open_conns", "open_conns", "in_use", "idle",
		"wait_count", "wait_duration_ms",
	} {
		if _, has := dbBlock[key]; !has {
			t.Errorf("db block missing %q field; got %v", key, dbBlock)
		}
	}
	if got, _ := dbBlock["max_open_conns"].(float64); got != 4 {
		t.Errorf("max_open_conns = %v, want 4 (matches db.maxOpenReadConns)", dbBlock["max_open_conns"])
	}
}
