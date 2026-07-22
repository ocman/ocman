package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ProxyEvents streams OpenCode's /event SSE to w until the upstream
// connection closes or ctx is cancelled.
func (a *Adapter) ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	port, session, err := a.resolvePort(sessionID)
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/event?directory=%s", port, url.QueryEscape(session.Directory))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("opencode events: %w", err)
	}
	// Invalidate the session cache when the SSE stream ends, regardless
	// of how it ends (clean EOF, client disconnect, or context cancel).
	// Without this, a user switching sessions and returning within the
	// cache TTL (5 s) would receive a stale snapshot that's missing
	// messages that arrived while they were away — the SSE stream was
	// closed so those events were never delivered, and the cache
	// prevents the reconcile fetch from picking them up.
	defer func() {
		sessionCache.invalidate(port, "/session/"+sessionID)
		sessionCache.invalidate(port, "/session/"+sessionID+"/message")
	}()

	// Use a client without a timeout for long-lived SSE connections.
	// Do NOT wrap with otelhttp.NewTransport here: the transport span
	// would span the entire streaming body read, and when the client
	// disconnects the context cancellation would mark that span as an
	// error — flooding Grafana with false positives. The parent
	// connection-lifetime span in handleSessionEvents already covers
	// the full SSE session and handles context.Canceled correctly.
	sseClient := &http.Client{Transport: a.auth.Transport(http.DefaultTransport)}
	resp, err := sseClient.Do(httpReq)
	if err != nil {
		forgetSessionPort(sessionID, port)
		return fmt.Errorf("opencode events connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		forgetSessionPort(sessionID, port)
		return fmt.Errorf("opencode events connect: upstream HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		forgetSessionPort(sessionID, port)
		return fmt.Errorf("opencode events connect: unexpected content-type %q", ct)
	}
	rememberSessionPort(sessionID, port)

	// OpenCode sends a server.heartbeat event every 10 seconds, so
	// under normal operation the read below unblocks well within this
	// window. The 60 s idle timeout exists to reclaim the goroutine
	// when the upstream TCP connection goes half-open (e.g. the
	// OpenCode process was killed without a clean FIN): the OS
	// keepalive would eventually fire, but 60 s is a tighter bound.
	// On timeout the body is closed, Read returns an error, and the
	// SSE handler's context-aware reconnect logic re-establishes.
	const sseIdleTimeout = 60 * time.Second
	var idleExpired atomic.Bool
	timer := time.AfterFunc(sseIdleTimeout, func() {
		idleExpired.Store(true)
		resp.Body.Close()
	})
	defer timer.Stop()

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			timer.Reset(sseIdleTimeout)
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			if flush != nil {
				flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			if idleExpired.Load() {
				return platforms.ErrSSEIdleTimeout
			}
			return readErr
		}
	}
}
