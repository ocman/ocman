package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
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
//
// resolvePortCtx is the request-scoped variant that records timings
// for the DB lookup and the port discovery phase via srvtiming. The
// non-ctx form is preserved for internal helpers that don't have a
// request context (e.g. background jobs).
func (a *Adapter) resolvePort(sessionID string) (port string, session *db.Session, err error) {
	return a.resolvePortCtx(context.Background(), sessionID)
}

func (a *Adapter) resolvePortCtx(ctx context.Context, sessionID string) (port string, session *db.Session, err error) {
	if a.db == nil {
		return "", nil, platforms.ErrNotFound
	}
	dbPhase := srvtiming.Begin(ctx, "db_get_session")
	s, err := a.db.GetSession(sessionID)
	dbPhase.End()
	if err != nil {
		return "", nil, platforms.ErrNotFound
	}
	port = resolveOpenCodePortForSessionCtx(ctx, sessionID, s.Directory)
	if port == "" {
		port = resolveOpenCodePortBySession(ctx, sessionID)
	}
	if port == "" {
		return "", s, fmt.Errorf("no running OpenCode instance for session %s: %w", sessionID, platforms.ErrPlatformUnreachable)
	}
	rememberSessionPort(sessionID, port)
	return port, s, nil
}

func resolveOpenCodePortBySession(ctx context.Context, sessionID string) string {
	client := http.Client{Timeout: 500 * time.Millisecond}
	for _, server := range discoverOpenCodeServers() {
		if server.port == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s/session/%s", server.port, sessionID), nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return server.port
		}
	}
	return ""
}

// --- Catalogs ---

// AgentCatalog returns the OpenCode /agent catalog for the session's
// running instance. Returns an empty slice when no instance is reachable.
//
// Cached via catalogCache: agents.json edits propagate after at most
// catalogCache's TTL, in exchange for not paying ~1s on every
// SessionDetail mount.
//
// Upstream failures (no live instance, /agent fetch fails, JSON
// decode fails) intentionally surface as `(nil, nil)` so the
// frontend keeps rendering an empty catalog. To make those failures
// observable to the maintainer, each branch logs a single WARN line
// (FR-9) including the upstream port + the underlying error.
func (a *Adapter) AgentCatalog(ctx context.Context, sessionID string) ([]platforms.AgentCatalogEntry, error) {
	port, _, err := a.resolvePortCtx(ctx, sessionID)
	if err != nil {
		// Common: no live OpenCode instance for this session's
		// directory. Logged at DEBUG to avoid spamming the noise
		// every poll.
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
			Debug("opencode: agent catalog unavailable (no live port)")
		return nil, nil
	}
	body, ok := getJSONCached(ctx, port, "/agent")
	if !ok {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "endpoint": "/agent"}).
			Warn("opencode: agent catalog fetch failed; returning empty list")
		return nil, nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "error": err}).
			Warn("opencode: agent catalog decode failed; returning empty list")
		return nil, nil
	}
	entries := make([]platforms.AgentCatalogEntry, 0, len(raw))
	for _, r := range raw {
		mode := stringField(r, "mode")
		entries = append(entries, platforms.AgentCatalogEntry{
			Name:        stringField(r, "name"),
			Description: stringField(r, "description"),
			Model:       stringField(r, "model"),
			Color:       stringField(r, "color"),
			Kind:        mode,
			Mode:        mode,
			Hidden:      boolField(r, "hidden"),
			BuiltIn:     boolField(r, "native"),
		})
	}
	return entries, nil
}

// SlashCommands returns the OpenCode /command catalog for the session.
//
// Cached via catalogCache for the same reason AgentCatalog is.
//
// Like [AgentCatalog], upstream failures return `(nil, nil)` for
// frontend compat but emit a single WARN line per failure (FR-9).
func (a *Adapter) SlashCommands(ctx context.Context, sessionID string) ([]platforms.SlashCommandEntry, error) {
	port, _, err := a.resolvePortCtx(ctx, sessionID)
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
			Debug("opencode: slash commands unavailable (no live port)")
		return nil, nil
	}
	body, ok := getJSONCached(ctx, port, "/command")
	if !ok {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "endpoint": "/command"}).
			Warn("opencode: slash commands fetch failed; returning empty list")
		return nil, nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "error": err}).
			Warn("opencode: slash commands decode failed; returning empty list")
		return nil, nil
	}
	entries := make([]platforms.SlashCommandEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, platforms.SlashCommandEntry{
			Name:        stringField(r, "name"),
			Description: stringField(r, "description"),
			Template:    stringField(r, "template"),
			Source:      stringField(r, "source"),
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
	dbPhase := srvtiming.Begin(ctx, "db_get_session")
	session, err := a.db.GetSession(sessionID)
	dbPhase.End()
	if err != nil {
		return nil, platforms.ErrNotFound
	}

	// recents and favorites are global (same answer regardless of
	// which session is open) and slowly-changing, so we route them
	// through process-global TTL caches with singleflight. See
	// models_cache.go for the rationale and TTL choices. The
	// session-default lookup below stays uncached because it's
	// per-session and already cheap.
	recentsPhase := srvtiming.Begin(ctx, "db_recent_models")
	recents, err := getRecentModelsCached(a.db)
	recentsPhase.End()
	if err != nil {
		log.WithError(err).Warn("opencode: fetching recent models")
	}
	defaultsPhase := srvtiming.Begin(ctx, "db_session_defaults")
	defaults, err := getSessionDefaultsCached(a.db, sessionID, session.Directory)
	defaultsPhase.End()
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Warn("opencode: fetching session defaults")
	}
	sessionDefault := defaults.Model

	var favorites []state.ModelFavorite
	if a.favorites != nil {
		favPhase := srvtiming.Begin(ctx, "db_favorites")
		favorites, err = a.favorites.ModelFavorites(string(PlatformID))
		favPhase.End()
		if err != nil {
			log.WithError(err).Warn("opencode: fetching model favorites")
		}
	}

	var providers OpenCodeProvidersResponse
	hasProviders := false
	if port := resolveOpenCodePortForSessionCtx(ctx, sessionID, session.Directory); port != "" {
		providersPhase := srvtiming.Begin(ctx, "http_provider")
		providers, hasProviders = fetchOpenCodeProviders(port)
		providersPhase.EndWithDesc("GET /provider")
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

// --- Mutating operations ---

// marshalRequest wraps json.Marshal with the shared error context used
// by every mutating operation in this file.
func marshalRequest(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return payload, nil
}

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
	payload, err := marshalRequest(body)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/session/%s/prompt_async", req.SessionID)
	if err := postJSON(ctx, port, path, payload); err != nil {
		var upstream *platforms.UpstreamError
		if errors.As(err, &upstream) {
			return err
		}
		forgetSessionPort(req.SessionID, port)
		InvalidateOpenCodePortCache()
		retryPort, _, retryResolveErr := a.resolvePortCtx(ctx, req.SessionID)
		if retryResolveErr == nil && retryPort != "" && retryPort != port {
			if retryErr := postJSON(ctx, retryPort, path, payload); retryErr == nil {
				rememberSessionPort(req.SessionID, retryPort)
				return nil
			}
		}
		return err
	}
	rememberSessionPort(req.SessionID, port)
	return nil
}

// RunShell executes a raw shell command via POST /session/{id}/shell,
// bypassing the LLM. OpenCode synthesises an assistant message whose
// only part is a `bash` tool with the command's stdout/stderr; no
// tokens are spent.
//
// `agent` is required by OpenCode's request schema; we default to
// "build" when the caller leaves it blank, matching the composer's
// `!`-prefix UX (chosen with the user — see commit history).
func (a *Adapter) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return runShellOnPort(ctx, port, req)
}

// runShellOnPort is the port-resolved core of RunShell, factored out so
// tests can drive it against an httptest server without standing up a
// full Adapter (which would need an OpenCode SQLite DB and a real
// running instance for lsof to discover).
func runShellOnPort(ctx context.Context, port string, req platforms.RunShellRequest) error {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return fmt.Errorf("opencode RunShell: command is required")
	}
	agent := req.Agent
	if agent == "" {
		agent = "build"
	}
	payload, err := marshalRequest(map[string]string{
		"command": command,
		"agent":   agent,
	})
	if err != nil {
		return err
	}
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/shell", req.SessionID), payload)
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
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/command", req.SessionID), payload)
}

// RespondPermission answers a pending permission prompt.
func (a *Adapter) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := marshalRequest(map[string]interface{}{"response": req.Reply})
	if err != nil {
		return err
	}
	if err := postJSON(ctx, port, fmt.Sprintf("/session/%s/permissions/%s", req.SessionID, req.PermissionID), payload); err != nil {
		return err
	}
	// The pending-prompt cache (3 s TTL) backs the sidebar's
	// pendingPermission flag. Without this invalidation, the sidebar
	// keeps showing the bell for up to ~6 s after auto-approve (one TTL
	// before the cache expires, plus one sidebar poll interval) — long
	// enough to feel like the auto-approve didn't fire. Drop the entry
	// so the next /api/sessions fan-out fetches a fresh list from
	// OpenCode without the just-resolved prompt.
	getPendingPromptCache().invalidate(port, "/permission")
	return nil
}

// RespondQuestion replies to a pending question prompt.
func (a *Adapter) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := marshalRequest(map[string]interface{}{"answers": req.Answers})
	if err != nil {
		return err
	}
	if err := postJSON(ctx, port, fmt.Sprintf("/question/%s/reply", req.RequestID), payload); err != nil {
		return err
	}
	// See RespondPermission: drop the cached /question list so the
	// sidebar's pendingQuestion flag clears immediately on the next poll.
	getPendingPromptCache().invalidate(port, "/question")
	return nil
}

// RejectQuestion dismisses a pending question prompt.
func (a *Adapter) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	if err := postJSON(ctx, port, fmt.Sprintf("/question/%s/reject", req.RequestID), []byte("{}")); err != nil {
		return err
	}
	getPendingPromptCache().invalidate(port, "/question")
	return nil
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
	payload, err := marshalRequest(map[string]string{"title": req.Title})
	if err != nil {
		return err
	}
	return patchJSON(ctx, port, fmt.Sprintf("/session/%s", req.SessionID), payload)
}

// PermissionRules reads the session's permission ruleset from
// OpenCode's GET /session/{id}. A missing/null `permission` field
// means the session inherits the configured defaults; that's
// returned as an empty slice.
func (a *Adapter) PermissionRules(_ context.Context, sessionID string) ([]platforms.PermissionRule, error) {
	port, _, err := a.resolvePort(sessionID)
	if err != nil {
		return nil, err
	}
	return permissionRulesOnPort(port, sessionID)
}

func permissionRulesOnPort(port, sessionID string) ([]platforms.PermissionRule, error) {
	// Bypass sessionCache: the toggle UI needs read-after-write freshness.
	body, ok := rawGet(port, "/session/"+sessionID)
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

// SetPermissionRules replaces the session's permission ruleset via
// PATCH /session/{id}. An empty ruleset matches nothing, so the
// session falls back to the configured defaults.
func (a *Adapter) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return setPermissionRulesOnPort(ctx, port, req)
}

func setPermissionRulesOnPort(ctx context.Context, port string, req platforms.SetPermissionRulesRequest) error {
	rules := req.Rules
	if rules == nil {
		rules = []platforms.PermissionRule{}
	}
	payload, err := json.Marshal(map[string]any{"permission": rules})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if err := patchJSON(ctx, port, fmt.Sprintf("/session/%s", req.SessionID), payload); err != nil {
		return err
	}
	sessionCache.invalidate(port, "/session/"+req.SessionID)
	return nil
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
	payload, err := marshalRequest(map[string]string{
		"providerID": providerID,
		"modelID":    modelID,
	})
	if err != nil {
		return err
	}
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/summarize", req.SessionID), payload)
}

// CreateSession creates a new OpenCode session bound to the given
// directory. Returns the new session ID.
func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	// When the caller already knows the instance's port (e.g. a
	// worktree session created on the project's single instance),
	// skip discovery entirely — no lsof scan.
	port := req.Port
	if port == "" {
		// Fast path: reuse the cached port map when opencode is already
		// running (the common case). Only fall back to a fresh uncached
		// lsof scan on a miss — that scan is multi-hundred-ms on macOS and
		// exists to catch a just-launched process the cache hasn't seen yet.
		portPhase := srvtiming.Begin(ctx, "lsof_fresh")
		port = discoverOpenCodePort(req.Directory)
		if port == "" {
			port = discoverOpenCodePortFresh(req.Directory)
		}
		portPhase.EndWithDesc("port discovery (cached, fresh on miss)")
	}
	if port == "" {
		// Log the requested dir (raw + normalized) against every
		// discovered opencode cwd so a path mismatch (symlinks, remote
		// vs hub path strings) is visible instead of a silent retry spin.
		log.WithFields(log.Fields{
			"requested":       req.Directory,
			"normalized":      normalizePortDirectory(req.Directory),
			"discovered_dirs": discoveredOpenCodeDirs(),
		}).Warn("no running OpenCode instance for directory")
		return nil, fmt.Errorf("no running OpenCode instance for directory %s: %w", req.Directory, platforms.ErrPlatformUnreachable)
	}

	createPhase := srvtiming.Begin(ctx, "http_create")
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session", port)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Root the new session at the requested directory. OpenCode honors
	// a per-session directory via the x-opencode-directory header, so a
	// single instance can create a session rooted at an external dir
	// (a worktree) different from the process launch cwd.
	if req.Directory != "" {
		// URL-encode so a path with spaces (or other bytes unsafe in a
		// raw header value) survives transit. OpenCode decodes with
		// decodeURIComponent; url.PathEscape is the matching encoder.
		httpReq.Header.Set("x-opencode-directory", url.PathEscape(req.Directory))
	}
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
	createPhase.EndWithDesc("opencode POST /session")

	// If a custom title was provided, set it immediately after creation.
	if req.Title != "" && parsed.ID != "" {
		titlePhase := srvtiming.Begin(ctx, "http_title")
		payload, err := marshalRequest(map[string]string{"title": req.Title})
		if err != nil {
			return nil, err
		}
		err = patchJSON(ctx, port, fmt.Sprintf("/session/%s", parsed.ID), payload)
		titlePhase.EndWithDesc("opencode PATCH /session/{id} (title)")
		if err != nil {
			log.WithError(err).Warn("failed to set custom title on new session")
			// Don't fail the entire creation if title setting fails.
		}
	}
	rememberSessionPort(parsed.ID, port)

	return &platforms.CreateSessionResponse{ID: parsed.ID}, nil
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

func boolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// Lightly-annotated alias so the public Platform interface doesn't leak
// the OpenCode-specific raw struct name.
