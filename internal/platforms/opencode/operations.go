package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// This file holds the Platform-interface operation methods that were
// formerly inlined in internal/server. Each method resolves the running
// OpenCode port from the session's working directory and proxies the
// call to the OpenCode HTTP API.

// resolvePort looks up the running OpenCode HTTP port for the given
// session. Returns the port and the session (so callers can use the
// directory for logging) or an error if the session isn't known or no
// instance is reachable.
func (a *Adapter) resolvePort(sessionID string) (port string, session *db.Session, err error) {
	if a.db == nil {
		return "", nil, platforms.ErrNotFound
	}
	s, err := a.db.GetSession(sessionID)
	if err != nil {
		return "", nil, platforms.ErrNotFound
	}
	port = discoverOpenCodePort(s.Directory)
	if port == "" {
		return "", s, fmt.Errorf("no running OpenCode instance for session %s: %w", sessionID, platforms.ErrPlatformUnreachable)
	}
	return port, s, nil
}

// --- Catalogs ---

// AgentCatalog returns the OpenCode /agent catalog for the session's
// running instance. Returns an empty slice when no instance is reachable.
func (a *Adapter) AgentCatalog(ctx context.Context, sessionID string) ([]platforms.AgentCatalogEntry, error) {
	port, _, err := a.resolvePort(sessionID)
	if err != nil {
		return nil, nil
	}
	body, ok := getJSON(ctx, port, "/agent")
	if !ok {
		return nil, nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil
	}
	entries := make([]platforms.AgentCatalogEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, platforms.AgentCatalogEntry{
			Name:        stringField(r, "name"),
			Description: stringField(r, "description"),
			Model:       stringField(r, "model"),
			Color:       stringField(r, "color"),
			Kind:        stringField(r, "kind"),
		})
	}
	return entries, nil
}

// SlashCommands returns the OpenCode /command catalog for the session.
func (a *Adapter) SlashCommands(ctx context.Context, sessionID string) ([]platforms.SlashCommandEntry, error) {
	port, _, err := a.resolvePort(sessionID)
	if err != nil {
		return nil, nil
	}
	body, ok := getJSON(ctx, port, "/command")
	if !ok {
		return nil, nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil
	}
	entries := make([]platforms.SlashCommandEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, platforms.SlashCommandEntry{
			Name:        stringField(r, "name"),
			Description: stringField(r, "description"),
			Template:    stringField(r, "template"),
		})
	}
	return entries, nil
}

// SessionModels builds the per-session model picker list, merging
// recents from the DB with live /provider data from the running
// instance when available.
func (a *Adapter) SessionModels(ctx context.Context, sessionID string) (*platforms.SessionModelsResponse, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	session, err := a.db.GetSession(sessionID)
	if err != nil {
		return nil, platforms.ErrNotFound
	}

	recents, err := a.db.GetRecentModels(50, 10)
	if err != nil {
		log.WithError(err).Warn("opencode: fetching recent models")
	}
	defaults, err := a.db.GetSessionDefaults(sessionID, session.Directory)
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Warn("opencode: fetching session defaults")
	}
	sessionDefault := defaults.Model

	var favorites []state.ModelFavorite
	if a.favorites != nil {
		favorites, err = a.favorites.ModelFavorites(string(PlatformID))
		if err != nil {
			log.WithError(err).Warn("opencode: fetching model favorites")
		}
	}

	var providers OpenCodeProvidersResponse
	hasProviders := false
	if port := discoverOpenCodePort(session.Directory); port != "" {
		providers, hasProviders = fetchOpenCodeProviders(port)
	}

	entries := buildSessionModelEntries(recents, favorites, providers, hasProviders, sessionDefault)

	var connectedDefaults map[string]string
	if hasProviders && len(providers.Default) > 0 {
		connectedDefaults = make(map[string]string, len(providers.Connected))
		for _, id := range providers.Connected {
			if m, ok := providers.Default[id]; ok {
				connectedDefaults[id] = m
			}
		}
	}

	return &platforms.SessionModelsResponse{
		SessionDefault:   sessionDefault,
		ProviderDefaults: connectedDefaults,
		HasProviders:     hasProviders,
		Models:           entries,
	}, nil
}

// ListPermissions returns pending permission prompts for the session's
// directory. Filters out prompts for other sessions — the frontend
// only cares about those it could act on.
func (a *Adapter) ListPermissions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return a.listPrompts(ctx, sessionID, "/permission")
}

// ListQuestions returns pending question prompts for the session.
func (a *Adapter) ListQuestions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return a.listPrompts(ctx, sessionID, "/question")
}

func (a *Adapter) listPrompts(ctx context.Context, sessionID, path string) ([]platforms.LivePrompt, error) {
	port, _, err := a.resolvePort(sessionID)
	if err != nil {
		return nil, nil
	}
	body, ok := getJSON(ctx, port, path)
	if !ok {
		return nil, nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil
	}
	out := make([]platforms.LivePrompt, 0, len(raw))
	for _, r := range raw {
		if sid, ok := r["sessionID"].(string); ok && sid != "" && sid != sessionID {
			continue
		}
		out = append(out, platforms.LivePrompt(r))
	}
	return out, nil
}

// --- Mutating operations ---

// SendMessage submits a composer message via /session/{id}/prompt_async.
func (a *Adapter) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
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
	payload, _ := json.Marshal(body)
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/prompt_async", req.SessionID), payload)
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
	payload, _ := json.Marshal(body)
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/command", req.SessionID), payload)
}

// RespondPermission answers a pending permission prompt.
func (a *Adapter) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]interface{}{"response": req.Reply})
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/permissions/%s", req.SessionID, req.PermissionID), payload)
}

// RespondQuestion replies to a pending question prompt.
func (a *Adapter) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]interface{}{"answers": req.Answers})
	return postJSON(ctx, port, fmt.Sprintf("/question/%s/reply", req.RequestID), payload)
}

// RejectQuestion dismisses a pending question prompt.
func (a *Adapter) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return postJSON(ctx, port, fmt.Sprintf("/question/%s/reject", req.RequestID), []byte("{}"))
}

// Abort cancels the in-flight response for a session.
func (a *Adapter) Abort(ctx context.Context, req platforms.AbortRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/abort", req.SessionID), []byte("{}"))
}

// RenameSession sets a new title for a session.
func (a *Adapter) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"title": req.Title})
	return patchJSON(ctx, port, fmt.Sprintf("/session/%s", req.SessionID), payload)
}

// Compact summarizes the session's history, preferring OpenCode's
// configured `small_model` when set.
func (a *Adapter) Compact(ctx context.Context, req platforms.CompactRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	providerID, modelID := req.ProviderID, req.ModelID
	if p, m, ok := fetchOpenCodeSmallModel(port); ok {
		providerID, modelID = p, m
	}
	payload, _ := json.Marshal(map[string]string{
		"providerID": providerID,
		"modelID":    modelID,
	})
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/summarize", req.SessionID), payload)
}

// CreateSession creates a new OpenCode session bound to the given
// directory. Returns the new session ID.
func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		return nil, fmt.Errorf("no running OpenCode instance for directory %s: %w", req.Directory, platforms.ErrPlatformUnreachable)
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session", port)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := openCodeClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("opencode create-session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("opencode create-session: upstream HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == "" {
		// Fall back to the raw body so callers can still see whatever
		// OpenCode returned; but if we got a parseable ID, prefer that.
		if len(body) == 0 {
			return nil, errors.New("opencode create-session: empty response")
		}
	}

	// If a custom title was provided, set it immediately after creation.
	if req.Title != "" && parsed.ID != "" {
		payload, _ := json.Marshal(map[string]string{"title": req.Title})
		if err := patchJSON(ctx, port, fmt.Sprintf("/session/%s", parsed.ID), payload); err != nil {
			log.WithError(err).Warn("failed to set custom title on new session")
			// Don't fail the entire creation if title setting fails.
		}
	}

	return &platforms.CreateSessionResponse{ID: parsed.ID}, nil
}

// ProxyEvents streams OpenCode's /event SSE to w until the upstream
// connection closes or ctx is cancelled.
func (a *Adapter) ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	port, _, err := a.resolvePort(sessionID)
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/event", port)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("opencode events: %w", err)
	}

	// Use a client without a timeout for long-lived SSE connections.
	sseClient := &http.Client{}
	resp, err := sseClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("opencode events connect: %w", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
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
			return readErr
		}
	}
}

// --- Helpers ---

// getJSON performs a GET to the OpenCode instance and returns the body
// bytes and true iff the response was 200 OK with a JSON content type.
func getJSON(ctx context.Context, port, path string) ([]byte, bool) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, false
	}
	resp, err := openCodeClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	return body, true
}

// postJSON performs a POST with a JSON body. Returns nil on 2xx,
// an error describing the upstream status otherwise.
func postJSON(ctx context.Context, port, path string, payload []byte) error {
	return sendJSON(ctx, http.MethodPost, port, path, payload)
}

// patchJSON performs a PATCH with a JSON body. Returns nil on 2xx,
// an error describing the upstream status otherwise.
func patchJSON(ctx context.Context, port, path string, payload []byte) error {
	return sendJSON(ctx, http.MethodPatch, port, path, payload)
}

// sendJSON performs an HTTP request with a JSON body. Returns nil on 2xx,
// an error describing the upstream status otherwise.
//
// On a 4xx response the returned error wraps a *platforms.UpstreamError
// so callers can pass the upstream-supplied human message through to
// the UI (errors.Is(err, platforms.ErrUpstreamRejected) will be true).
// 5xx and transport errors fall through as plain wrapped errors and
// land in the default "platform unreachable" bucket on the way out.
func sendJSON(ctx context.Context, method, port, path string, payload []byte) error {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, method, apiURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := openCodeClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			ue := &platforms.UpstreamError{
				Status:  resp.StatusCode,
				Message: extractOpenCodeErrorMessage(body),
			}
			return fmt.Errorf("opencode %s: %w", path, ue)
		}
		if len(body) == 0 {
			return fmt.Errorf("opencode %s: upstream HTTP %d", path, resp.StatusCode)
		}
		return fmt.Errorf("opencode %s: upstream HTTP %d: %s", path, resp.StatusCode, string(body))
	}
	return nil
}

// extractOpenCodeErrorMessage best-effort parses an OpenCode error
// response body into a single human-readable string suitable for the
// UI. OpenCode's API errors follow the Hono `NamedError` shape:
//
//	{"data":{"providerID":"...","modelID":"..."},"name":"ProviderModelNotFoundError"}
//
// We prefer `data.message` if present (it's already a complete
// sentence), then fall back to combining `name` with any structured
// `data` fields, and finally to the raw body. Returns "" when the
// body is empty so callers can apply their own fallback.
func extractOpenCodeErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Name string                 `json:"name"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Name == "" {
		return trimmed
	}
	if msg, ok := parsed.Data["message"].(string); ok && msg != "" {
		return msg
	}
	// Build "<Name>: k=v, k=v" from the structured data so callers
	// see something useful for errors that don't carry a message
	// (e.g. ProviderModelNotFoundError → "providerID=anthropic, modelID=claude-bogus").
	if len(parsed.Data) == 0 {
		return parsed.Name
	}
	keys := make([]string, 0, len(parsed.Data))
	for k := range parsed.Data {
		keys = append(keys, k)
	}
	// Stable order without importing sort: the data maps in practice
	// have <=3 keys; a tiny bubble keeps the test deterministic and
	// avoids the extra import.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, parsed.Data[k]))
	}
	return fmt.Sprintf("%s: %s", parsed.Name, strings.Join(parts, ", "))
}

// parseOpenCodeModelRefInternal is the non-exported version used by
// SendMessage so the adapter doesn't depend on the server package.
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

func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Lightly-annotated alias so the public Platform interface doesn't leak
// the OpenCode-specific raw struct name.
var _ = sync.WaitGroup{} // keep sync imported for the session fetcher
