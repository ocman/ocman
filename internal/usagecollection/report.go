package usagecollection

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// endpoint rejects credentials, query strings and redirects. Plain HTTP is only
// allowed for loopback development collectors; public reports require TLS.
func endpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return ""
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return ""
	}
	u.Path = "/v1/usage"
	return u.String()
}

func (m *Module) sendPending(ctx context.Context) {
	m.mu.Lock()
	if m.deps.Store == nil || ctx.Err() != nil || m.now().Before(m.nextAttempt) {
		m.mu.Unlock()
		return
	}
	enabled, err := m.enabled(ctx)
	if err != nil || !enabled {
		if err == nil {
			_ = m.clearPending(ctx)
		}
		m.mu.Unlock()
		return
	}
	url := endpoint(m.deps.RelayURL())
	if url == "" {
		m.mu.Unlock()
		return
	}
	report, err := m.pending(ctx, m.now())
	if err != nil || report == nil {
		m.mu.Unlock()
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	m.cancel = cancel
	m.nextAttempt = m.now().Add(15 * time.Minute)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.cancel = nil
		m.mu.Unlock()
	}()

	body, err := json.Marshal(report)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ocman-usage")
	resp, err := m.client.Do(req)
	if err != nil {
		return
	} // Never log payloads, collector responses or request URLs.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.acknowledge(ctx, report)
}
