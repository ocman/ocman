package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/platforms"
)

// RespondPermission answers a pending permission prompt.
func (a *Adapter) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	port, session, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := marshalRequest(map[string]interface{}{"reply": req.Reply})
	if err != nil {
		return err
	}
	directory := session.Directory
	promptSessionID := req.SessionID
	path := fmt.Sprintf("/permission/%s/reply", req.PermissionID)
	v2 := false
	if entry, ok := a.prompts.find("permission", req.PermissionID); ok {
		directory = entry.directory
		promptSessionID = entry.prompt.StringField("sessionID")
		_, v2 = entry.prompt["action"]
		if v2 {
			path = fmt.Sprintf("/api/session/%s/permission/%s/reply", promptSessionID, req.PermissionID)
		}
	}
	if v2 {
		err = a.postJSON(ctx, port, path, payload)
	} else {
		err = a.postJSONForDirectory(ctx, port, path, directory, payload)
	}
	if err != nil && !isUpstreamNotFound(err) {
		return err
	}
	// A missing prompt was already answered or aborted. Forget it locally too.
	a.ObservePromptResolved(directory, "permission", promptSessionID, req.PermissionID)
	return err
}

func isUpstreamNotFound(err error) bool {
	var upstream *platforms.UpstreamError
	return errors.As(err, &upstream) && upstream.Status == http.StatusNotFound
}

// RespondQuestion replies to a pending question prompt.
func (a *Adapter) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	port, session, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := marshalRequest(map[string]interface{}{"answers": req.Answers})
	if err != nil {
		return err
	}
	directory := session.Directory
	promptSessionID := req.SessionID
	if entry, ok := a.prompts.find("question", req.RequestID); ok {
		directory = entry.directory
		promptSessionID = entry.prompt.StringField("sessionID")
	}
	err = a.postJSONForDirectory(ctx, port, fmt.Sprintf("/question/%s/reply", req.RequestID), directory, payload)
	if err != nil && !isUpstreamNotFound(err) {
		return err
	}
	a.ObservePromptResolved(directory, "question", promptSessionID, req.RequestID)
	return err
}

// RejectQuestion dismisses a pending question prompt.
func (a *Adapter) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	port, session, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	directory := session.Directory
	promptSessionID := req.SessionID
	if entry, ok := a.prompts.find("question", req.RequestID); ok {
		directory = entry.directory
		promptSessionID = entry.prompt.StringField("sessionID")
	}
	err = a.postJSONForDirectory(ctx, port, fmt.Sprintf("/question/%s/reject", req.RequestID), directory, []byte("{}"))
	if err != nil && !isUpstreamNotFound(err) {
		return err
	}
	a.ObservePromptResolved(directory, "question", promptSessionID, req.RequestID)
	return err
}

// PermissionRules reads the session's permission ruleset. Missing rules inherit
// configured defaults and are represented by an empty slice.
func (a *Adapter) PermissionRules(_ context.Context, sessionID string) ([]platforms.PermissionRule, error) {
	port, _, err := a.resolvePort(sessionID)
	if err != nil {
		return nil, err
	}
	return a.permissionRulesOnPort(port, sessionID)
}

func (h *httpState) permissionRulesOnPort(port, sessionID string) ([]platforms.PermissionRule, error) {
	body, ok := h.rawGet(port, "/session/"+sessionID)
	if !ok {
		return nil, fmt.Errorf("opencode session fetch failed: %w", platforms.ErrPlatformUnreachable)
	}
	var parsed struct {
		Permission []platforms.PermissionRule `json:"permission"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decoding session permission: %w", err)
	}
	if parsed.Permission == nil {
		return []platforms.PermissionRule{}, nil
	}
	return parsed.Permission, nil
}

// SetPermissionRules replaces the session's permission ruleset.
func (a *Adapter) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return a.setPermissionRulesOnPort(ctx, port, req)
}

func (h *httpState) setPermissionRulesOnPort(ctx context.Context, port string, req platforms.SetPermissionRulesRequest) error {
	rules := req.Rules
	if rules == nil {
		rules = []platforms.PermissionRule{}
	}
	payload, err := json.Marshal(map[string]any{"permission": rules})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if err := h.patchJSON(ctx, port, fmt.Sprintf("/session/%s", req.SessionID), payload); err != nil {
		return err
	}
	return nil
}
