// Package gui provides the Wails desktop-app wrapper for ocman.
//
// When ocman is started with --gui, RunGUIWithStarter is called instead of the plain
// HTTP server. It starts the full server stack on a random loopback port and
// then opens a native WebView window pointing at it.  The existing HTTP
// handler (including the embedded static FS and all API routes) is reused
// verbatim — no duplicate asset embedding or routing logic here.
//
// The window links Wails only with the gui build tag (wails.json passes it to
// wails build and wails dev). Headless builds get a RunGUIWithStarter that
// returns ErrNoGUI.
package gui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
)

// bootstrapHandler answers the WebView's initial wails:// load. It does not
// proxy: it sends the WebView to the real backend URL, so every later
// request (assets, API POSTs, SSE, the terminal WebSocket) talks to the
// loopback server directly with an http Host and Origin the server's
// host allowlist and CSRF checks already accept. Proxying through the Wails
// asset server instead presents Host "wails", a wails:// Origin and an
// X-Forwarded-For, and cannot carry WebSocket upgrades at all.
type bootstrapHandler struct {
	backend string
	timeout time.Duration
}

func newBootstrapHandler(backend string, timeout time.Duration) *bootstrapHandler {
	return &bootstrapHandler{backend: backend, timeout: timeout}
}

func (h *bootstrapHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Wait first: the backend boots in OnStartup, concurrently with this
	// load, and a navigation to a dead backend leaves WKWebView blank,
	// whereas this bounded wait yields a readable page with a retry.
	if err := waitForServer(h.backend, h.timeout); err != nil {
		log.WithError(err).WithField("backend", h.backend).Error("gui: backend unreachable")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>ocman</title>
<body style="background:#0f0f14;color:#ddd;font:14px system-ui;padding:48px">
<p>The ocman backend at %s is not responding.</p>
<p><a style="color:#8ab4f8" href="#" onclick="location.reload()">Retry</a></p></body>`, html.EscapeString(h.backend))
		return
	}
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>ocman</title>
<body style="background:#0f0f14"><script>location.replace("%s/")</script></body>`, template.JSEscapeString(h.backend))
}

// loopbackURL turns the bound listener address into the URL the WebView
// loads. An unspecified bind (0.0.0.0 / ::) is reachable on loopback.
func loopbackURL(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return "http://" + addr.String()
	}
	ip := tcp.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.IPv4(127, 0, 0, 1)
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(tcp.Port))
}

// singleInstanceID derives the Wails single-instance lock ID from the
// state DB path: two app launches sharing one state.db collapse into one
// process, while a different state DB may run alongside.
func singleInstanceID(stateDBPath string) string {
	if abs, err := filepath.Abs(stateDBPath); err == nil {
		stateDBPath = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(stateDBPath)))
	return "ocman-" + hex.EncodeToString(sum[:8])
}

// waitForServer waits for the startup UI, not migration completion. Older
// standalone servers without that endpoint still use /api/auth/me.
func waitForServer(base string, timeout time.Duration) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	last := "no response"
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/startup")
		if err == nil && resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			resp, err = client.Get(base + "/api/auth/me")
		}
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("gui: backend at %s not ready after %s (%s)", base, timeout, last)
}
