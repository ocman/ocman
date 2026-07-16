package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

// ── handler validation (no tmux required) ────────────────────────────
//
// These exercise the request-validation and method-dispatch paths that
// run before any tmux interaction.

func TestValidDir(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		ok   bool
	}{
		{"absolute ok", "/home/u/proj", true},
		{"empty rejected", "", false},
		{"relative rejected", "proj/sub", false},
		{"dot rejected", ".", false},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		if got := validDir(rr, c.dir); got != c.ok {
			t.Errorf("%s: validDir(%q) = %v, want %v (body=%q)",
				c.name, c.dir, got, c.ok, rr.Body.String())
		}
		if !c.ok && rr.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400 on invalid dir, got %d", c.name, rr.Code)
		}
	}
}

// The sub-handlers run their request validation before any tmux call,
// so we can assert the validation paths directly regardless of whether
// a tmux binary is present (the availability guard lives in the parent
// dispatcher handleTermWindows).

func TestHandleTermWindowsList_BadDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/term/windows?dir=relative", nil)
	rr := httptest.NewRecorder()
	srv.handleTermWindowsList(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTermWindowsList_MissingDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/term/windows", nil)
	rr := httptest.NewRecorder()
	srv.handleTermWindowsList(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing dir, got %d", rr.Code)
	}
}

func TestHandleTermWindowsCreate_BadDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/term/windows",
		strings.NewReader(`{"dir":"relative"}`))
	rr := httptest.NewRecorder()
	srv.handleTermWindowsCreate(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTermWindowsDelete_BadDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/term/windows",
		strings.NewReader(`{"dir":"relative","window":"ocman-a3a758e833-1"}`))
	rr := httptest.NewRecorder()
	srv.handleTermWindowsDelete(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTermWindows_MethodNotAllowed(t *testing.T) {
	if !tmux.IsAvailable() {
		t.Skip("tmux not available; dispatcher returns 503 before method check")
	}
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPut, "/api/term/windows", nil)
	rr := httptest.NewRecorder()
	srv.handleTermWindows(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for PUT, got %d", rr.Code)
	}
}

func TestHandleTermWindowsDelete_RejectsCrossDirWindow(t *testing.T) {
	srv := &Server{}
	// A window name whose hash doesn't match the supplied (valid,
	// absolute) dir must be rejected as not-found, never killed — this
	// is the guard that stops cross-project / arbitrary kills. The
	// mismatch short-circuits before any tmux call, so this runs
	// without a tmux binary.
	dir := t.TempDir()
	foreign := "ocman-deadbeef00-1" // valid shape, wrong hash for dir
	body := `{"dir":"` + dir + `","window":"` + foreign + `"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/term/windows",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleTermWindowsDelete(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-dir window, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ── wsTermConn frame classification ──────────────────────────────────

// TestWSTermConn_FrameClassification drives a real WebSocket through
// wsTermConn: a resize JSON text frame becomes a Resize frame, other
// text is a keystroke, binary is a keystroke, and PTY output written
// back arrives as a binary frame. This is the parsing logic the terminal
// bridge depends on.
func TestWSTermConn_FrameClassification(t *testing.T) {
	// Server upgrades and echoes each Recv result as a labelled string so
	// the client can assert the classification, then writes a byte back.
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		c, err := termUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		tc := newWSTermConn(c)
		defer tc.Close()

		// Three inbound frames.
		want := []string{"resize:80x24", "data:ls\n", "data:\x01\x02"}
		for _, exp := range want {
			f, err := tc.Recv()
			if err != nil {
				t.Errorf("Recv: %v", err)
				return
			}
			var got string
			if f.Resize != nil {
				got = "resize:" + strconv.Itoa(int(f.Resize.Cols)) + "x" + strconv.Itoa(int(f.Resize.Rows))
			} else {
				got = "data:" + string(f.Data)
			}
			if got != exp {
				t.Errorf("frame = %q, want %q", got, exp)
			}
		}
		// PTY output back to the viewer.
		if err := tc.Write([]byte("OUT")); err != nil {
			t.Errorf("Write: %v", err)
		}
	}))
	defer srv.Close()

	// CheckOrigin is loopback-only; httptest binds 127.0.0.1 so a bare
	// dial with no Origin header passes.
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	defer ws.Close()

	// resize (text JSON), keystroke (text), keystroke (binary).
	_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":80,"rows":24}`))
	_ = ws.WriteMessage(websocket.TextMessage, []byte("ls\n"))
	_ = ws.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02})

	// PTY output comes back as a binary frame.
	mt, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if mt != websocket.BinaryMessage || string(data) != "OUT" {
		t.Fatalf("echo = type %d %q, want binary \"OUT\"", mt, data)
	}
	<-done
}

func TestTermWebSocketRejectsForeignOrigin(t *testing.T) {
	s := &Server{}
	h := s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		conn, err := termUpgrader.Upgrade(w, r, nil)
		if err == nil {
			conn.Close()
		}
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	header := http.Header{"Origin": []string{"https://evil.example"}}
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), header)
	if conn != nil {
		conn.Close()
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		t.Fatal("foreign-origin WebSocket unexpectedly connected")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign-origin status = %#v, want 403", resp)
	}
}
