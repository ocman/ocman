package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// --- test helpers ---

func execLookPath(name string) (string, error) { return exec.LookPath(name) }

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Suppress the "hint:" output from modern git about default branch naming etc.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
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
	req := httptest.NewRequest("GET", "/api/system/stats", nil)
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
// pattern documented in docs/profiling.md: if `wait_count` ever
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
	req := httptest.NewRequest("GET", "/api/system/stats", nil)
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


