package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

var _ platforms.NativeQueue = (*Adapter)(nil)

// v2Port resolves the session's server and requires it to be OpenCode
// v2, the only version with a native follow-up queue (its inbox).
func (a *Adapter) v2Port(ctx context.Context, sessionID string) (string, error) {
	// Cheap check first: on a v1 machine this runs on every queue change.
	if !ocv2.InstalledV2() {
		return "", platforms.ErrUnsupported
	}
	port, _, err := a.resolvePortCtx(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if !ocv2.IsV2(ctx, port) {
		return "", platforms.ErrUnsupported
	}
	return port, nil
}

// NativeQueued lists the prompts OpenCode v2 holds for the session's
// next idle boundary (inbox items with delivery "queue").
func (a *Adapter) NativeQueued(ctx context.Context, sessionID string) ([]platforms.NativeQueuedMessage, error) {
	port, err := a.v2Port(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	body, err := getJSON(ctx, port, "/api/session/"+url.PathEscape(sessionID)+"/inbox")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Delivery string `json:"delivery"`
			Time     struct {
				Created int64 `json:"created"`
			} `json:"time"`
			Payload struct {
				Text  string            `json:"text"`
				Files []json.RawMessage `json:"files"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding OpenCode inbox: %w", err)
	}
	out := []platforms.NativeQueuedMessage{}
	for _, item := range resp.Data {
		if item.Type != "user" || item.Delivery != "queue" {
			continue
		}
		out = append(out, platforms.NativeQueuedMessage{
			ID: item.ID, Text: item.Payload.Text, HasImages: len(item.Payload.Files) > 0, CreatedAt: item.Time.Created,
		})
	}
	return out, nil
}

// CancelNativeQueued drops a held prompt from the session's inbox.
func (a *Adapter) CancelNativeQueued(ctx context.Context, req platforms.CancelNativeQueuedRequest) error {
	port, err := a.v2Port(ctx, req.SessionID)
	if err != nil {
		return err
	}
	err = sendJSON(ctx, http.MethodDelete, port,
		"/api/session/"+url.PathEscape(req.SessionID)+"/inbox/"+url.PathEscape(req.ID), nil)
	if isUpstreamNotFound(err) {
		return nil // already delivered or cancelled
	}
	return err
}
