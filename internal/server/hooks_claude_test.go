package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	claudecodeplatform "github.com/NoUseFreak/ocman/internal/platforms/claudecode"
)

// newServerWithClaudeCode returns a *Server with an empty OpenCode DB
// (via testServer) plus a Claude Code adapter pointed at a temp
// directory so Available() is true and the hook handler will accept.
func newServerWithClaudeCode(t *testing.T) *Server {
	t.Helper()
	srv := testServer(t)
	// Pointing at the test's temp dir guarantees Available() = true
	// and that ApplyHookEvent has a live cache to write into.
	tmp := t.TempDir()
	srv.registry.Register(claudecodeplatform.NewFromDir(tmp))
	return srv
}

func TestHandleClaudeHook_AcceptsValidPayload(t *testing.T) {
	srv := newServerWithClaudeCode(t)
	body := strings.NewReader(`{
		"session_id":"S1",
		"hook_event_name":"UserPromptSubmit",
		"cwd":"/tmp/p"
	}`)
	req := httptest.NewRequest("POST", "/api/hooks/claude", body)
	req.RemoteAddr = "127.0.0.1:12345" // pass requireLocalhost
	rr := httptest.NewRecorder()
	srv.handleClaudeHook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true (full body %+v)", got["ok"], got)
	}
	if got["ignored"] != nil && got["ignored"] != false {
		t.Errorf("ignored = %v, want false/nil", got["ignored"])
	}
}

func TestHandleClaudeHook_UnknownEventIsIgnoredButOK(t *testing.T) {
	srv := newServerWithClaudeCode(t)
	body := strings.NewReader(`{"session_id":"S","hook_event_name":"UnknownFutureEvent"}`)
	req := httptest.NewRequest("POST", "/api/hooks/claude", body)
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	srv.handleClaudeHook(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (hooks must not fail CLI commands)", rr.Code)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if got["ignored"] != true {
		t.Errorf("ignored = %v, want true", got["ignored"])
	}
}

func TestHandleClaudeHook_MalformedJSONReturns400(t *testing.T) {
	srv := newServerWithClaudeCode(t)
	body := strings.NewReader(`{broken`)
	req := httptest.NewRequest("POST", "/api/hooks/claude", body)
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	srv.handleClaudeHook(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed JSON", rr.Code)
	}
}

// TestHandleClaudeHook_NoAdapterReturns204 covers the case where the
// Claude Code adapter isn't registered (e.g. ocman booted on a box
// without ~/.claude). The handler must stay 2xx — Claude CLI users
// shouldn't see their hooks fail just because ocman wasn't built
// with the claude-code feature.
func TestHandleClaudeHook_NoAdapterReturns204(t *testing.T) {
	srv := testServer(t) // only opencode registered
	body := strings.NewReader(`{"session_id":"S1","hook_event_name":"Stop"}`)
	req := httptest.NewRequest("POST", "/api/hooks/claude", body)
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	srv.handleClaudeHook(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 when no CC adapter registered", rr.Code)
	}
}

// TestHandleClaudeHook_UpdatesLiveStatus wires the whole chain:
// POST /api/hooks/claude -> adapter cache -> LiveStatus. This is the
// integration-level check that hooks actually flow through.
func TestHandleClaudeHook_UpdatesLiveStatus(t *testing.T) {
	srv := newServerWithClaudeCode(t)
	adapter, ok := srv.registry.Get(claudecodeplatform.PlatformID)
	if !ok {
		t.Fatal("claude-code adapter missing from registry")
	}

	// Baseline: no live state.
	if adapter.LiveStatus("S1") != nil {
		t.Fatal("expected nil LiveStatus before any hook event")
	}

	body := strings.NewReader(`{"session_id":"S1","hook_event_name":"UserPromptSubmit"}`)
	req := httptest.NewRequest("POST", "/api/hooks/claude", body)
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	srv.handleClaudeHook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want 200", rr.Code)
	}

	ls := adapter.LiveStatus("S1")
	if ls == nil {
		t.Fatal("expected LiveStatus populated after hook")
	}
	if ls.Status != "busy" {
		t.Errorf("status = %q, want busy", ls.Status)
	}
}

// TestHandleClaudeHook_RejectsNonLocalhost is a defence-in-depth
// check: even if someone exposes ocman on 0.0.0.0, a remote hook
// source must not be able to mutate session state.
func TestHandleClaudeHook_RejectsNonLocalhost(t *testing.T) {
	srv := newServerWithClaudeCode(t)
	body := strings.NewReader(`{"session_id":"S1","hook_event_name":"Stop"}`)
	req := httptest.NewRequest("POST", "/api/hooks/claude", body)
	req.RemoteAddr = "10.0.0.42:12345"
	rr := httptest.NewRecorder()

	// requireLocalhost is applied at the mux level; exercise the
	// whole pipeline by wrapping the handler the same way.
	requireLocalhost(srv.handleClaudeHook)(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for non-loopback remote", rr.Code)
	}
	// Live state must not have been touched.
	adapter, _ := srv.registry.Get(claudecodeplatform.PlatformID)
	if ls := adapter.LiveStatus("S1"); ls != nil {
		t.Errorf("expected no live state after rejected request, got %+v", ls)
	}
}

// Satisfy the context import — used indirectly through testServer.
var _ = context.Background
