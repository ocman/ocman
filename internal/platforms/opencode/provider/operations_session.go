package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/platforms"
)

// Abort cancels the in-flight response for a session.
func (a *Adapter) Abort(ctx context.Context, req platforms.AbortRequest) error {
	if req.TaskID != "" {
		return a.stopBackgroundTask(ctx, req)
	}
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return a.postJSON(ctx, port, fmt.Sprintf("/session/%s/abort", req.SessionID), []byte("{}"))
}

// DisposeSession permanently removes a session and its history.
func (a *Adapter) DisposeSession(ctx context.Context, req platforms.DisposeSessionRequest) error {
	port := req.Port
	if port == "" {
		var err error
		port, _, err = a.resolvePort(req.SessionID)
		if err != nil {
			return err
		}
	}
	path := fmt.Sprintf("/session/%s", req.SessionID)
	if err := a.sendJSON(ctx, http.MethodDelete, port, path, nil); err != nil && !isUpstreamNotFound(err) {
		return err
	}
	a.rt.forgetSessionPort(req.SessionID, port)
	return nil
}

// Revert restores the session and working tree to before MessageID.
func (a *Adapter) Revert(ctx context.Context, req platforms.RevertSessionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := marshalRequest(map[string]string{"messageID": req.MessageID})
	if err != nil {
		return err
	}
	if err := a.postJSON(ctx, port, fmt.Sprintf("/session/%s/revert", req.SessionID), payload); err != nil {
		return err
	}
	return nil
}

// Unrevert restores all messages previously reverted in a session.
func (a *Adapter) Unrevert(ctx context.Context, req platforms.UnrevertSessionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	if err := a.postJSON(ctx, port, fmt.Sprintf("/session/%s/unrevert", req.SessionID), []byte("{}")); err != nil {
		return err
	}
	return nil
}

// RenameSession sets a new title for a session.
func (a *Adapter) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := marshalRequest(map[string]string{"title": req.Title})
	if err != nil {
		return err
	}
	return a.patchJSON(ctx, port, fmt.Sprintf("/session/%s", req.SessionID), payload)
}

// Compact summarizes history, preferring the configured small_model.
func (a *Adapter) Compact(ctx context.Context, req platforms.CompactRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	providerID, modelID := req.ProviderID, req.ModelID
	if p, m, ok := a.fetchOpenCodeSmallModel(port); ok {
		providerID, modelID = p, m
	}
	payload, err := marshalRequest(map[string]string{"providerID": providerID, "modelID": modelID})
	if err != nil {
		return err
	}
	return a.postJSONLLM(ctx, port, fmt.Sprintf("/session/%s/summarize", req.SessionID), payload)
}

// ForkSession branches a session, optionally from a specific message.
func (a *Adapter) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return nil, err
	}
	payloadMap := map[string]string{}
	if req.MessageID != "" {
		payloadMap["messageID"] = req.MessageID
	}
	payload, err := marshalRequest(payloadMap)
	if err != nil {
		return nil, err
	}
	body, err := a.postJSONReturning(ctx, port, fmt.Sprintf("/session/%s/fork", req.SessionID), payload)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == "" {
		return nil, fmt.Errorf("opencode fork: unexpected response %q", string(body))
	}
	a.rt.rememberSessionPort(parsed.ID, port)
	return &platforms.CreateSessionResponse{ID: parsed.ID}, nil
}

// MoveSession relocates a session to another project directory.
func (a *Adapter) MoveSession(ctx context.Context, req platforms.MoveSessionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"sessionID": req.SessionID, "destination": map[string]string{"directory": req.Directory}})
	if err != nil {
		return err
	}
	if err := a.postJSON(ctx, port, "/experimental/control-plane/move-session", payload); err != nil {
		return err
	}
	return nil
}
