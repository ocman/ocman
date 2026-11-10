package opencode

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
)

// SendMessage submits a composer message via POST /session/{id}/prompt_async.
// The v2 transport handles agent/model switches and delivery.
func (a *Adapter) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	lockIndex := crc32.ChecksumIEEE([]byte(req.SessionID)) % uint32(len(a.sendLock))
	sendMu := &a.sendLock[lockIndex]
	sendMu.Lock()
	defer sendMu.Unlock()
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	err = a.sendMessageOnPort(ctx, port, req)
	if err == nil {
		a.rt.rememberSessionPort(req.SessionID, port)
		return nil
	}
	var upstream *platforms.UpstreamError
	if errors.As(err, &upstream) || errors.Is(err, platforms.ErrUnsupported) {
		return err
	}
	a.rt.forgetSessionPort(req.SessionID, port)
	a.rt.InvalidateOpenCodePortCache()
	retryPort, _, retryResolveErr := a.resolvePortCtx(ctx, req.SessionID)
	if retryResolveErr == nil && retryPort != "" && retryPort != port {
		if retryErr := a.sendMessageOnPort(ctx, retryPort, req); retryErr == nil {
			a.rt.rememberSessionPort(req.SessionID, retryPort)
			return nil
		}
	}
	return err
}

func (h *httpState) sendMessageOnPort(ctx context.Context, port string, req platforms.SendMessageRequest) error {
	err := h.sendMessageLegacy(ctx, port, req)
	// A held prompt requiring a selection switch was not admitted by v2.
	var upstream *platforms.UpstreamError
	if req.Delivery != "" && errors.As(err, &upstream) && upstream.Status == http.StatusPreconditionFailed {
		return platforms.ErrUnsupported
	}
	return err
}

func (h *httpState) sendMessageLegacy(ctx context.Context, port string, req platforms.SendMessageRequest) error {
	parts := make([]map[string]string, 0, 1+len(req.Images))
	if req.Message != "" {
		parts = append(parts, map[string]string{"type": "text", "text": req.Message})
	}
	for _, img := range req.Images {
		parts = append(parts, map[string]string{"type": "file", "url": img.URL, "mime": img.Mime})
	}
	body := map[string]interface{}{"parts": parts}
	if mr := parseOpenCodeModelRefInternal(req.Model); mr != nil {
		if req.Reasoning != "" {
			mr.Variant = req.Reasoning
		}
		body["model"] = mr
	}
	if req.Agent != "" {
		body["agent"] = req.Agent
	}
	if req.Delivery != "" {
		body["delivery"] = req.Delivery
	}
	payload, err := marshalRequest(body)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/session/%s/prompt_async", req.SessionID)
	if err := h.postJSON(ctx, port, path, payload); err != nil {
		return err
	}
	return nil
}

// RunShell executes a raw shell command without a model request.
func (a *Adapter) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return a.runShellOnPort(ctx, port, req)
}

func (h *httpState) runShellOnPort(ctx context.Context, port string, req platforms.RunShellRequest) error {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return fmt.Errorf("opencode RunShell: command is required")
	}
	agent := req.Agent
	if agent == "" {
		agent = "build"
	}
	payload, err := marshalRequest(map[string]string{"command": command, "agent": agent})
	if err != nil {
		return err
	}
	return h.postJSON(ctx, port, fmt.Sprintf("/session/%s/shell", req.SessionID), payload)
}

// ExecuteCommand runs a slash command via /session/{id}/command.
func (a *Adapter) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	body := map[string]interface{}{"command": req.Command, "arguments": req.Arguments}
	if req.Model != "" {
		body["model"] = req.Model
	}
	if req.Agent != "" {
		body["agent"] = req.Agent
	}
	payload, err := marshalRequest(body)
	if err != nil {
		return err
	}
	// Model calls use the caller's deadline, not the 10s read client.
	return a.postJSONLLM(ctx, port, fmt.Sprintf("/session/%s/command", req.SessionID), payload)
}

type openCodeModelRefInternal struct {
	ProviderID string `json:"providerID,omitempty"`
	ModelID    string `json:"modelID"`
	Variant    string `json:"variant,omitempty"`
}

func parseOpenCodeModelRefInternal(model string) *openCodeModelRefInternal {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	providerID, modelID, ok := strings.Cut(model, "/")
	if ok {
		providerID = strings.TrimSpace(providerID)
		modelID = strings.TrimSpace(modelID)
		if providerID != "" && modelID != "" {
			return &openCodeModelRefInternal{ProviderID: providerID, ModelID: modelID}
		}
	}
	return &openCodeModelRefInternal{ModelID: model}
}
