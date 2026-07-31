package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestHandleSessionShell_DispatchesToAdapter exercises the happy path:
// POST /api/session/{id}/shell with a non-empty `command` is routed to
// the resolved adapter's RunShell, which returns nil; the handler
// responds 204 No Content.
func TestHandleSessionShell_DispatchesToAdapter(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	called := false
	fakeSessions := []db.Session{
		{ID: "fk-1", Platform: "fake", Directory: "/x", TimeUpdated: 1},
	}
	fp := &fakePlatform{
		id:       "fake",
		sessions: fakeSessions,
		runShell: func() error { called = true; return nil },
	}
	srv.registry.Register(fp)
	srv.registry.RememberSessions("fake", fakeSessions)

	body := strings.NewReader(`{"command":"echo hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/fk-1/shell", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Error("RunShell was not invoked on the adapter")
	}
}

// TestHandleSessionShell_RejectsEmptyCommand asserts the 400 short-
// circuit so the upstream platform never sees an empty `!` submission.
func TestHandleSessionShell_RejectsEmptyCommand(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	called := false
	fakeSessions := []db.Session{
		{ID: "fk-1", Platform: "fake", Directory: "/x", TimeUpdated: 1},
	}
	fp := &fakePlatform{
		id:       "fake",
		sessions: fakeSessions,
		runShell: func() error { called = true; return nil },
	}
	srv.registry.Register(fp)
	srv.registry.RememberSessions("fake", fakeSessions)

	for _, body := range []string{`{}`, `{"command":""}`, `{"command":"   "}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/session/fk-1/shell", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.dispatchSessionSubpath(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body=%s: expected 400, got %d: %s", body, rr.Code, rr.Body.String())
		}
	}
	if called {
		t.Error("RunShell was invoked despite empty command")
	}
}

// TestHandleSessionShell_UnsupportedReturns501 verifies that adapters
// without ShellExec support surface ErrUnsupported as
// 501 Not Implemented — same contract as ExecuteCommand on platforms
// without a slash-command catalog.
func TestHandleSessionShell_UnsupportedReturns501(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	fakeSessions := []db.Session{
		{ID: "fk-1", Platform: "fake", Directory: "/x", TimeUpdated: 1},
	}
	// runShell left nil → fakePlatform returns ErrUnsupported.
	srv.registry.Register(&fakePlatform{id: "fake", sessions: fakeSessions})
	srv.registry.RememberSessions("fake", fakeSessions)

	body := strings.NewReader(`{"command":"echo hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/fk-1/shell", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestCapabilities_ShellExec verifies that the OpenCode adapter
// reports caps.shellExec=true on the wire so the frontend can light
// up `!`-prefix routing without branching on platform identity.
func TestCapabilities_ShellExec(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	rr := httptest.NewRecorder()
	srv.handleCapabilities(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Platforms []struct {
			ID           string                 `json:"id"`
			Capabilities platforms.Capabilities `json:"capabilities"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, p := range resp.Platforms {
		if p.ID == "opencode" {
			found = true
			if !p.Capabilities.ShellExec {
				t.Errorf("OpenCode caps.shellExec = false, want true")
			}
		}
	}
	if !found {
		t.Fatal("opencode platform missing from /api/capabilities")
	}
}
