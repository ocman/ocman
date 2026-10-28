package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func (a *Adapter) bindActiveSessionPort(id, port, status string) {
	if a.rt != nil && turnRunning(status) {
		a.rt.rememberSessionPort(id, port)
	}
}

func (a *Adapter) sessionRuntimePort(id string) string {
	if port := a.rt.preferredSessionPort(id); port != "" {
		return port
	}
	if a.turns != nil {
		a.turns.mu.RLock()
		entry, ok := a.turns.entries[id]
		a.turns.mu.RUnlock()
		if ok && entry.port != "" {
			return entry.port
		}
	}
	return ""
}

// A creation or verified native-ID probe pins the session's transport. Project
// metadata is only a discovery hint when that binding must be reconstructed.
func (d *Discovery) resolveSessionPortCtx(ctx context.Context, id, directory string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if port := d.preferredSessionPort(id); port != "" {
		return port, nil
	}
	port, err := d.resolveOpenCodePortBySession(ctx, id)
	if err != nil {
		return "", err
	}
	if port == "" {
		candidate := d.discoverOpenCodePortCtx(ctx, directory)
		if candidate != "" {
			var found bool
			found, err = probeSessionPort(ctx, candidate, id)
			if found {
				port = candidate
			}
		}
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	d.rememberSessionPort(id, port)
	return port, nil
}

func probeSessionPort(ctx context.Context, port, id string) (bool, error) {
	client := http.Client{Timeout: 500 * time.Millisecond, Transport: openCodeClient.Transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, url.PathEscape(id)), nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ocapi.ErrAuthentication) {
			return false, err
		}
		return false, ctx.Err()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, nil
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&session); err != nil {
		return false, nil
	}
	return session.ID == id, nil
}
