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
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

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

// shutdownWait bounds how long quitting waits for the server's graceful
// shutdown: its own 5s HTTP drain plus headroom for plugin teardown.
const shutdownWait = 6 * time.Second

// App holds Wails lifecycle state.
type App struct {
	ctx   context.Context
	start func() // boots the backend; runs only in the instance that holds the lock
	stop  context.CancelFunc
	done  chan struct{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.start()
}

// focus brings the existing window forward when the app is launched again.
func (a *App) focus(options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

// shutdown runs on Cmd+Q / window close: cancel the server context so the
// graceful path in StartOnListener runs, and wait for it (bounded).
func (a *App) shutdown(context.Context) {
	a.stop()
	select {
	case <-a.done:
	case <-time.After(shutdownWait):
		log.Warn("gui: backend did not shut down in time")
	}
}

// RunGUI starts the ocman HTTP server on an ephemeral loopback port and opens
// a Wails window that loads it.  srv must already be fully constructed
// (New + registered adapters + auth) but not yet started.
func RunGUI(ctx context.Context, srv *server.Server, listenAddr, stateDBPath string, bootTimeout time.Duration) error {
	// Pick an ephemeral port for the backend so the GUI can point at it.
	// We override the address to 127.0.0.1:0 via a net.Listener, then
	// read back the actual port before starting Wails.
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("gui: listen: %w", err)
	}
	backendURL := loopbackURL(ln.Addr())
	log.WithField("addr", backendURL).Info("gui: backend listening")

	// The server starts from OnStartup, which Wails only reaches after the
	// single-instance check: a second launch exits before it ever runs
	// plugins, routines or the MCP bind. serverCtx derives from the signal
	// context, so SIGINT/SIGTERM and Cmd+Q share one graceful path. A
	// failure leaves the listener bound but unserved, so it is fatal
	// rather than a hang.
	serverCtx, stop := context.WithCancel(ctx)
	defer stop()
	app := &App{stop: stop, done: make(chan struct{})}
	app.start = func() {
		go func() {
			defer close(app.done)
			err := srv.StartOnListener(serverCtx, ln)
			if err != nil && serverCtx.Err() == nil {
				Fatalf("gui: backend server error: %v", err)
			}
			log.WithError(err).Info("gui: backend stopped")
		}()
	}

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
			Handler: newBootstrapHandler(backendURL, bootTimeout),
		},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 20, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               singleInstanceID(stateDBPath),
			OnSecondInstanceLaunch: app.focus,
		},
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
