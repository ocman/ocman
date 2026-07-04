package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/term"
)

// termUpgrader upgrades /api/term/ws to a WebSocket. The endpoint is
// localhost-only (gated by requireLocalhost in the route table), so the
// CheckOrigin allowlist is intentionally narrow: same-origin loopback
// only. Combined with requireLocalhost this keeps the PTY bridge
// unreachable from other origins or the network.
var termUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return isLoopback(r)
	},
}

// termResize is the only control message the client sends as text JSON.
// All other client->server frames are raw keystrokes forwarded to the
// PTY verbatim.
type termResize struct {
	Type string `json:"type"` // "resize"
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// wsTermConn adapts a browser WebSocket to hostsvc.TermConn: text frames
// are parsed as resize control (or treated as keystrokes when not the
// resize JSON), binary frames are keystrokes, and Write sends PTY output
// back as binary. It is the transport the local and remote hosts share.
type wsTermConn struct {
	conn *websocket.Conn
}

func newWSTermConn(conn *websocket.Conn) *wsTermConn { return &wsTermConn{conn: conn} }

func (c *wsTermConn) Recv() (hostsvc.TermFrame, error) {
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return hostsvc.TermFrame{}, err
		}
		switch mt {
		case websocket.TextMessage:
			var rz termResize
			if json.Unmarshal(data, &rz) == nil && rz.Type == "resize" {
				return hostsvc.TermFrame{Resize: &hostsvc.TermSize{Cols: rz.Cols, Rows: rz.Rows}}, nil
			}
			return hostsvc.TermFrame{Data: data}, nil
		case websocket.BinaryMessage:
			return hostsvc.TermFrame{Data: data}, nil
		}
	}
}

func (c *wsTermConn) Write(p []byte) error {
	return c.conn.WriteMessage(websocket.BinaryMessage, p)
}

func (c *wsTermConn) Close() error { return c.conn.Close() }

// handleTermWS attaches a browser xterm.js terminal to a dedicated
// terminal window in the single `ocman` tmux session.
//
// All terminal windows live in one session (term.SessionName). Each
// WebSocket attaches a PTY directly to that session and selects the
// requested window. The session is configured with `window-size
// manual` so each window can be sized independently per viewer via
// `resize-window`, driven from the browser's reported xterm dimensions
// — so one small browser tab can't shrink another tab's window.
//
// Query params:
//   - dir:      the OpenCode session's working directory (required).
//   - window:   a specific window name to attach to (optional; when
//     omitted the first window for dir is reused, or one is created).
//   - readonly: "1" to attach read-only (tmux attach -r).
//
// Mounted with requireLocalhost; this is a live shell, do not expose it
// on a non-loopback bind without rethinking auth.
func (s *Server) handleTermWS(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" || !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}

	// Resolve the owning host: a remote project's terminal must open a
	// shell on the remote machine, not the hub (R-C). ForRemote wins when
	// the client names an owner; otherwise fall back to dir resolution.
	host := s.router().ForDir(dir)
	if rid := r.URL.Query().Get("remoteId"); rid != "" {
		host = s.router().ForRemote(rid)
	}

	windowName := r.URL.Query().Get("window")
	if windowName != "" && !term.IsWindowForDir(windowName, dir) {
		// Explicit window must be a well-formed terminal window for dir,
		// so a caller can't target arbitrary windows.
		http.Error(w, "invalid window", http.StatusBadRequest)
		return
	}
	readonly := r.URL.Query().Get("readonly") == "1"

	conn, err := termUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		log.WithError(err).Debug("term ws upgrade failed")
		return
	}
	defer conn.Close()

	tc := newWSTermConn(conn)
	err = host.TermAttach(r.Context(), hostsvc.TermAttachRequest{
		Dir:      dir,
		Window:   windowName,
		Readonly: readonly,
	}, tc)
	if err != nil {
		log.WithError(err).WithField("dir", dir).Debug("terminal attach ended")
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "terminal error"))
	}
}

// ── REST: /api/term/windows ──────────────────────────────────────────

// termWindowsRequest is the shared shape for the terminal-window REST
// endpoints. `dir` is the OpenCode session directory the windows belong
// to; `window` (DELETE only) is the window to kill.
type termWindowsRequest struct {
	Dir      string `json:"dir"`
	Window   string `json:"window"`
	RemoteID string `json:"remoteId"`
}

// handleTermWindows is the multi-method handler for /api/term/windows:
//
//	GET    ?dir=<dir>      -> list terminal windows for dir
//	POST   {dir}           -> create a new terminal window for dir
//	DELETE {dir,window}    -> kill a terminal window
//
// All variants are localhost-only (wired with requireLocalhost). Every
// window lives in the single `ocman` session and is named so only
// windows belonging to dir are listed or killed.
func (s *Server) handleTermWindows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleTermWindowsList(w, r)
	case http.MethodPost:
		s.handleTermWindowsCreate(w, r)
	case http.MethodDelete:
		s.handleTermWindowsDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTermWindowsList(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if !validDir(w, dir) {
		return
	}
	host := s.router().ForDir(dir)
	if rid := r.URL.Query().Get("remoteId"); rid != "" {
		host = s.router().ForRemote(rid)
	}
	windows, err := host.TermWindows(r.Context(), dir)
	if err != nil {
		serverError(w, "listing terminal windows", err)
		return
	}
	if windows == nil {
		windows = []hostsvc.TermWindow{}
	}
	writeJSON(w, map[string]any{"windows": windows})
}

func (s *Server) handleTermWindowsCreate(w http.ResponseWriter, r *http.Request) {
	var req termWindowsRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validDir(w, req.Dir) {
		return
	}
	host := s.router().ForDir(req.Dir)
	if req.RemoteID != "" {
		host = s.router().ForRemote(req.RemoteID)
	}
	name, err := host.TermCreateWindow(r.Context(), req.Dir)
	if err != nil {
		serverError(w, "creating terminal window", err)
		return
	}
	log.WithFields(log.Fields{"window": name, "dir": req.Dir}).
		Info("created terminal window")
	writeJSON(w, map[string]any{"window": name})
}

func (s *Server) handleTermWindowsDelete(w http.ResponseWriter, r *http.Request) {
	var req termWindowsRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validDir(w, req.Dir) {
		return
	}
	// The window must belong to *this* dir's terminal set, so a caller
	// can't kill arbitrary windows. Existence is checked on the owner.
	if !term.IsWindowForDir(req.Window, req.Dir) {
		http.Error(w, "terminal window not found", http.StatusNotFound)
		return
	}
	host := s.router().ForDir(req.Dir)
	if req.RemoteID != "" {
		host = s.router().ForRemote(req.RemoteID)
	}
	if err := host.TermKillWindow(r.Context(), req.Dir, req.Window); err != nil {
		serverError(w, "killing terminal window", err)
		return
	}
	log.WithFields(log.Fields{"window": req.Window, "dir": req.Dir}).
		Info("killed terminal window")
	w.WriteHeader(http.StatusNoContent)
}

// validDir validates the dir param shared by the terminal-window
// endpoints, writing a 400 and returning false on failure.
func validDir(w http.ResponseWriter, dir string) bool {
	if dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return false
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return false
	}
	return true
}
