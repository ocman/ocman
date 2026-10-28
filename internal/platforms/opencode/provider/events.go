package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
)

// ProxyEvents streams OpenCode's /event SSE to w until the upstream
// connection closes or ctx is cancelled.
func (a *Adapter) ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	port, session, err := a.resolvePortCtx(ctx, sessionID)
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/event?directory=%s", port, url.QueryEscape(session.Directory))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("opencode events: %w", err)
	}
	// Use a client without a timeout for long-lived SSE connections.
	// Do NOT wrap with otelhttp.NewTransport here: the transport span
	// would span the entire streaming body read, and when the client
	// disconnects the context cancellation would mark that span as an
	// error — flooding Grafana with false positives. The parent
	// connection-lifetime span of the conversation stream already covers
	// the full SSE session and handles context.Canceled correctly.
	sseClient := &http.Client{Transport: a.auth.Transport(http.DefaultTransport)}
	resp, err := sseClient.Do(httpReq)
	if err != nil {
		a.rt.forgetSessionPort(sessionID, port)
		return fmt.Errorf("opencode events connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		a.rt.forgetSessionPort(sessionID, port)
		return fmt.Errorf("opencode events connect: upstream HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		a.rt.forgetSessionPort(sessionID, port)
		return fmt.Errorf("opencode events connect: unexpected content-type %q", ct)
	}
	a.rt.rememberSessionPort(sessionID, port)

	// On idle timeout the body is closed, Read returns an error, and the
	// SSE handler's context-aware reconnect logic re-establishes.
	idle := platforms.NewIdleWatchdog(platforms.SSEIdleTimeout, func() { resp.Body.Close() })
	defer idle.Stop()
	body := idle.Reader(resp.Body)

	buf := make([]byte, 4096)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
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
			if idle.Expired() {
				return platforms.ErrSSEIdleTimeout
			}
			return readErr
		}
	}
}
