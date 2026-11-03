//go:build gui

package gui

import (
	"context"
	"fmt"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

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

// RunGUIWithStarter starts the ocman HTTP server via start on an ephemeral
// loopback port and opens a Wails window that loads it. The window stays
// alive while its backend reloads in place.
func RunGUIWithStarter(ctx context.Context, start func(context.Context, net.Listener) error, listenAddr, stateDBPath string, bootTimeout time.Duration) error {
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
			err := start(serverCtx, ln)
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
