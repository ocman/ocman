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
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/NoUseFreak/ocman/internal/server"
)

// proxyHandler forwards every request to the running ocman HTTP server.
// Wails calls ServeHTTP for both the embedded assets and the /api/* routes,
// so a single reverse-proxy that points at the full server handles everything.
type proxyHandler struct {
	proxy *httputil.ReverseProxy
}

func newProxyHandler(target *url.URL) *proxyHandler {
	return &proxyHandler{proxy: httputil.NewSingleHostReverseProxy(target)}
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.proxy.ServeHTTP(w, r)
}

// App holds Wails lifecycle state.
type App struct {
	ctx context.Context
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// RunGUI starts the ocman HTTP server on an ephemeral loopback port and opens
// a Wails window that proxies to it.  srv must already be fully constructed
// (New + registered adapters + auth) but not yet started.
func RunGUI(ctx context.Context, srv *server.Server, listenAddr string) error {
	// Pick an ephemeral port for the backend so the GUI can point at it.
	// We override the address to 127.0.0.1:0 via a net.Listener, then
	// read back the actual port before starting Wails.
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("gui: listen: %w", err)
	}
	actualAddr := ln.Addr().String()
	backendURL, err := url.Parse("http://" + actualAddr)
	if err != nil {
		return fmt.Errorf("gui: parse backend URL: %w", err)
	}

	log.WithField("addr", actualAddr).Info("gui: backend listening")

	// Start the server on the pre-bound listener in a background goroutine.
	// The context passed here is the same signal context used in CLI mode,
	// so SIGINT/SIGTERM still trigger a graceful shutdown.
	go func() {
		if err := srv.StartOnListener(ctx, ln); err != nil {
			log.WithError(err).Error("gui: backend server error")
		}
	}()

	// Give the server a moment to finish its startup bookkeeping (hook
	// installation, background loops) before Wails opens the window and
	// fires the first HTTP request.
	waitForServer(backendURL.String(), 3*time.Second)

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
			// No embedded FS: all requests (assets + /api) are proxied to
			// the running HTTP server, which already handles both.
			Handler: newProxyHandler(backendURL),
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

// waitForServer polls the backend until it responds or the timeout expires.
func waitForServer(base string, timeout time.Duration) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/stats")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Warn("gui: backend did not respond within timeout; opening window anyway")
}
