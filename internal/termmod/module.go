// Package termmod is the feature module for the in-app browser terminals:
// the /api/term/windows controls and the /api/term/ws WebSocket bridge.
//
// It is the hub-side transport only. The terminal itself (the named tmux
// server, windows, PTY) is internal/term, reached through the owning
// machine's hostsvc.Host, so a remote project's shell runs on the remote.
// That is why this is not part of internal/term: term runs on the owner,
// this package routes to any owner and must not drag HTTP into it.
package termmod

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/term"
)

// Deps is what the server supplies.
type Deps struct {
	// ResolveOwner resolves an explicit or inferred owner. It writes the
	// response and returns false when an explicit remote is not connected.
	ResolveOwner func(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool)
	// Privileged is the server's privileged-request check (loopback peer,
	// browser origin, auth), used as the WebSocket CheckOrigin.
	Privileged func(*http.Request) bool
}

// Module owns the /api/term routes.
type Module struct {
	deps     Deps
	upgrader websocket.Upgrader
}

func NewModule(deps Deps) *Module {
	return &Module{deps: deps, upgrader: websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return deps.Privileged != nil && deps.Privileged(r)
		},
	}}
}

func (m *Module) Name() string { return "terminals" }

// Routes are localhost-only: both are a live shell. The WS upgrade is a
// GET that hijacks the connection, so it has no method wrapper. The windows
// routes list (GET ?dir=), create (POST {dir}) or kill (DELETE {dir,window})
// the terminal windows of dir; only windows named for dir are listed or
// killed.
func (m *Module) Routes(r module.Router) {
	r.Local("/api/term/ws", m.handleWS)
	r.Local("GET /api/term/windows", m.listWindows)
	r.Local("POST /api/term/windows", m.createWindow)
	r.Local("DELETE /api/term/windows", m.killWindow)
}

// handleWS attaches a browser xterm.js terminal to a terminal window of
// dir on its owner.
//
// Query params: dir (required, absolute), window (optional; must be a
// terminal window of dir, otherwise the first one is reused or created),
// readonly ("1" attaches read-only), remoteId (explicit owner).
func (m *Module) handleWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir := q.Get("dir")
	if dir == "" || !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}
	// Resolve before the upgrade so a rejected owner gets a plain HTTP
	// error instead of a WebSocket that opens a hub shell.
	host, ok := m.deps.ResolveOwner(w, dir, q.Get("remoteId"))
	if !ok {
		return
	}
	windowName := q.Get("window")
	if windowName != "" && !term.IsWindowForDir(windowName, dir) {
		http.Error(w, "invalid window", http.StatusBadRequest)
		return
	}

	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		log.WithError(err).Debug("term ws upgrade failed")
		return
	}
	defer conn.Close()
	// Shutdown cancels the request but never closes a hijacked socket; an
	// idle terminal would otherwise block in Recv and hold the server's
	// handler join open. Expiring reads unblocks it and still lets the
	// close frame below go out; the deferred Close releases the socket.
	stopReads := context.AfterFunc(r.Context(), func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stopReads()

	err = host.TermAttach(r.Context(), hostsvc.TermAttachRequest{
		Dir: dir, Window: windowName, Readonly: q.Get("readonly") == "1",
	}, &wsConn{conn: conn})
	if err != nil {
		log.WithError(err).WithField("dir", dir).Debug("terminal attach ended")
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "terminal error"))
	}
}

// wsConn adapts a browser WebSocket to hostsvc.TermConn: a resize JSON
// text frame is a resize, any other frame is keystrokes, and PTY output
// goes back as binary frames.
type wsConn struct{ conn *websocket.Conn }

type resizeMsg struct {
	Type string `json:"type"` // "resize"
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (c *wsConn) Recv() (hostsvc.TermFrame, error) {
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return hostsvc.TermFrame{}, err
		}
		switch mt {
		case websocket.TextMessage:
			var rz resizeMsg
			if json.Unmarshal(data, &rz) == nil && rz.Type == "resize" {
				return hostsvc.TermFrame{Resize: &hostsvc.TermSize{Cols: rz.Cols, Rows: rz.Rows}}, nil
			}
			return hostsvc.TermFrame{Data: data}, nil
		case websocket.BinaryMessage:
			return hostsvc.TermFrame{Data: data}, nil
		}
	}
}

func (c *wsConn) Write(p []byte) error { return c.conn.WriteMessage(websocket.BinaryMessage, p) }

func (c *wsConn) Close() error { return c.conn.Close() }

// windowsRequest is the body of POST and DELETE /api/term/windows.
type windowsRequest struct {
	Dir      string `json:"dir"`
	Window   string `json:"window"`
	RemoteID string `json:"remoteId"`
}

func (m *Module) listWindows(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if !validDir(w, dir) {
		return
	}
	host, ok := m.deps.ResolveOwner(w, dir, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	windows, err := host.TermWindows(r.Context(), dir)
	if err != nil {
		httpx.ServerError(w, "listing terminal windows", err)
		return
	}
	if windows == nil {
		windows = []hostsvc.TermWindow{}
	}
	httpx.WriteJSON(w, map[string]any{"windows": windows})
}

func (m *Module) createWindow(w http.ResponseWriter, r *http.Request) {
	var req windowsRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) || !validDir(w, req.Dir) {
		return
	}
	host, ok := m.deps.ResolveOwner(w, req.Dir, req.RemoteID)
	if !ok {
		return
	}
	name, err := host.TermCreateWindow(r.Context(), req.Dir)
	if err != nil {
		httpx.ServerError(w, "creating terminal window", err)
		return
	}
	log.WithFields(log.Fields{"window": name, "dir": req.Dir}).Info("created terminal window")
	httpx.WriteJSON(w, map[string]any{"window": name})
}

func (m *Module) killWindow(w http.ResponseWriter, r *http.Request) {
	var req windowsRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) || !validDir(w, req.Dir) {
		return
	}
	// The window must belong to this dir, so a caller can't kill arbitrary
	// windows. Existence is checked on the owner.
	if !term.IsWindowForDir(req.Window, req.Dir) {
		http.Error(w, "terminal window not found", http.StatusNotFound)
		return
	}
	host, ok := m.deps.ResolveOwner(w, req.Dir, req.RemoteID)
	if !ok {
		return
	}
	if err := host.TermKillWindow(r.Context(), req.Dir, req.Window); err != nil {
		httpx.ServerError(w, "killing terminal window", err)
		return
	}
	log.WithFields(log.Fields{"window": req.Window, "dir": req.Dir}).Info("killed terminal window")
	w.WriteHeader(http.StatusNoContent)
}

// validDir writes a 400 and returns false unless dir is absolute.
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
