// Package gui provides the Wails desktop-app wrapper for ocman.
//
// When ocman is started with --gui, RunGUI is called instead of the plain
// HTTP server. It starts the full server stack on a random loopback port and
// then opens a native WebView window pointing at it.  The existing HTTP
// handler (including the embedded static FS and all API routes) is reused
// verbatim — no duplicate asset embedding or routing logic here.
package gui

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/NoUseFreak/ocman/internal/server"
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
	client  *http.Client
}

func newBootstrapHandler(backend string) *bootstrapHandler {
	return &bootstrapHandler{backend: backend, client: &http.Client{Timeout: 10 * time.Second}}
}

func (h *bootstrapHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Probe first: a navigation to a dead backend leaves WKWebView blank,
	// whereas this bounded check yields a readable page with a retry.
	resp, err := h.client.Get(h.backend + "/")
	if err == nil {
		resp.Body.Close()
	}
	if err != nil || resp.StatusCode >= 500 {
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

// App holds Wails lifecycle state.
type App struct {
	ctx context.Context
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// RunGUI starts the ocman HTTP server on an ephemeral loopback port and opens
// a Wails window that loads it.  srv must already be fully constructed
// (New + registered adapters + auth) but not yet started.
func RunGUI(ctx context.Context, srv *server.Server, listenAddr string, bootTimeout time.Duration) error {
	// Pick an ephemeral port for the backend so the GUI can point at it.
	// We override the address to 127.0.0.1:0 via a net.Listener, then
	// read back the actual port before starting Wails.
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("gui: listen: %w", err)
	}
	backendURL := loopbackURL(ln.Addr())
	log.WithField("addr", backendURL).Info("gui: backend listening")

	// Start the server on the pre-bound listener in a background goroutine.
	// The context passed here is the same signal context used in CLI mode,
	// so SIGINT/SIGTERM still trigger a graceful shutdown. A failure leaves
	// the listener bound but unserved, so it is fatal rather than a hang.
	go func() {
		if err := srv.StartOnListener(ctx, ln); err != nil {
			Fatalf("gui: backend server error: %v", err)
		}
	}()

	// Don't open a window on a dead backend.
	if err := waitForServer(backendURL, bootTimeout); err != nil {
		return err
	}

	app := &App{}

	// platformOptions() is defined in app_darwin.go / app_linux.go /
	// app_other.go and injects OS-specific Wails window options.
	opts := &options.App{
		Title:     "ocman",
		Width:     1400,
		Height:    900,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			// No embedded FS: the handler only redirects the WebView to
			// the running HTTP server, which serves assets and /api.
			Handler: newBootstrapHandler(backendURL),
		},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 20, A: 255},
		OnStartup:        app.startup,
	}
	platformOptions(opts)

	if err := wails.Run(opts); err != nil {
		return fmt.Errorf("gui: wails: %w", err)
	}
	return nil
}

// waitForServer polls the backend until /api/auth/me (cheap, reachable
// without a login) answers 200, or fails once timeout expires.
func waitForServer(base string, timeout time.Duration) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	last := "no response"
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/auth/me")
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
