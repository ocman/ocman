package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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
		return "", s, fmt.Errorf("no running OpenCode instance for session %s: %w", sessionID, platforms.ErrPlatformUnreachable)
	}
	return port, s, nil
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
	port, _, err := a.resolvePortCtx(ctx, sessionID)
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
	// Subagent prompts carry the subagent's session ID, not the parent's.
	// Bubble them up so the parent session's UI can render and respond to
	// them — otherwise OpenCode stalls waiting on a prompt the user can't
	// see (the subagent sessions are hidden from the listing).
	//
	// We're already on the live path (resolvePort succeeded), so prefer
	// OpenCode's GET /session/:id/children over the read-only DB. This
	// removes a DB hit from RespondPermission's neighbour code and keeps
	// the live mutating path API-pure. Falls back to the DB on upstream
	// failure so prompts still bubble when, e.g., OpenCode briefly drops
	// the children endpoint (older versions, transient errors).
	subagentIDs := fetchSubagentSessionIDs(ctx, port, sessionID)
	if subagentIDs == nil && a.db != nil {
		subagentIDs, _ = a.db.GetSubagentSessionIDs(sessionID)
	}
	return filterPromptsForSession(raw, sessionID, subagentIDs), nil
}

// fetchSubagentSessionIDs calls GET /session/:id/children on the
// running OpenCode instance and returns the IDs of every direct child
// (subagent) session. Returns nil on any upstream failure so callers
// can fall back to the DB lookup — the result is best-effort UI
// plumbing, never a hard dependency.
//
// Routed through catalogCache: a parent session's children list
// changes only when a new subagent spawns, which is rare enough on
// the timescale of a single dashboard poll that the 30s TTL is
// fine. This also keeps the SSE-driven listPrompts polling cheap
// when multiple sessions on the same instance are in flight.
func fetchSubagentSessionIDs(ctx context.Context, port, sessionID string) []string {
	body, ok := getJSONCached(ctx, port, fmt.Sprintf("/session/%s/children", sessionID))
	if !ok {
		return nil
	}
	return parseSubagentChildIDs(body)
}

// parseSubagentChildIDs extracts the `id` field of every entry in
// OpenCode's GET /session/:id/children response. Permissive: ignores
// entries with an empty/missing id and returns nil for malformed
// payloads (so listPrompts's nil-check triggers the DB fallback
// rather than treating "broken upstream" as "no children").
func parseSubagentChildIDs(body []byte) []string {
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		if id, ok := entry["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// filterPromptsForSession returns the subset of OpenCode prompt entries
// (from /permission or /question) that belong to the given session or any
// of its subagents. Entries without a sessionID are kept as-is — older
// OpenCode versions emit parent-scoped prompts that way.
//
// Kept as a pure function so the inclusion logic is testable without
// spinning up an HTTP server or running OpenCode.
func filterPromptsForSession(raw []map[string]interface{}, sessionID string, subagentIDs []string) []platforms.LivePrompt {
	allowed := make(map[string]bool, 1+len(subagentIDs))
	allowed[sessionID] = true
	for _, id := range subagentIDs {
		allowed[id] = true
	}
	out := make([]platforms.LivePrompt, 0, len(raw))
	for _, r := range raw {
		sid, hasSID := r["sessionID"].(string)
		if hasSID && sid != "" && !allowed[sid] {
			continue
		}
		out = append(out, platforms.LivePrompt(r))
	}
	return out
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
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
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
	payload, err := json.Marshal(map[string]string{
		"command": command,
		"agent":   agent,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
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
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/command", req.SessionID), payload)
}

// RespondPermission answers a pending permission prompt.
func (a *Adapter) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]interface{}{"response": req.Reply})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
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
	payload, err := json.Marshal(map[string]interface{}{"answers": req.Answers})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
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
	payload, err := json.Marshal(map[string]string{"title": req.Title})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
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
	payload, err := json.Marshal(map[string]string{
		"providerID": providerID,
		"modelID":    modelID,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/summarize", req.SessionID), payload)
}

// CreateSession creates a new OpenCode session bound to the given
// directory. Returns the new session ID.
func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	portPhase := srvtiming.Begin(ctx, "lsof_fresh")
	port := discoverOpenCodePortFresh(req.Directory)
	portPhase.EndWithDesc("fresh lsof port discovery")
	if port == "" {
		return nil, fmt.Errorf("no running OpenCode instance for directory %s: %w", req.Directory, platforms.ErrPlatformUnreachable)
	}

	createPhase := srvtiming.Begin(ctx, "http_create")
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
	createPhase.EndWithDesc("opencode POST /session")

	// If a custom title was provided, set it immediately after creation.
	if req.Title != "" && parsed.ID != "" {
		titlePhase := srvtiming.Begin(ctx, "http_title")
		payload, err := json.Marshal(map[string]string{"title": req.Title})
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
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
	sseClient := &http.Client{Transport: http.DefaultTransport}
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

// --- Helpers ---

// catalogCache is a process-wide TTL cache for upstream OpenCode
// catalog endpoints (/agent, /command, /provider). 30s is the
// trade-off between staleness when the user edits config and
// per-poll cost: at 30s a cold dashboard mount fires one upstream
// call per endpoint, every subsequent mount within the window is
// instant.
//
// The cache is keyed by (port, path), so multiple running OpenCode
// instances coexist correctly. See httpcache.go for the cache
// machinery itself, including the singleflight that coalesces
// concurrent misses for the same key.
var catalogCache = newHTTPCacheNamed(30*time.Second, "opencode.catalog_http")

// sessionCache is a process-wide TTL cache for session-scoped
// OpenCode endpoints — currently /session/{id} and
// /session/{id}/message. It exists to absorb the multi-handler
// fan-out that happens when the user opens a session detail page:
// /api/session/{id} fetches both endpoints, and /api/session/{id}/info
// fetches /session/{id}/message a second time, in parallel. Without
// caching that's 3 simultaneous round-trips for the same session.
//
// 5s TTL is the trade-off between freshness and the bursty
// "user clicks around" pattern: the dashboard fires several
// per-session requests within a short window when the panel mounts,
// then nothing for a few seconds, then another burst when the user
// clicks somewhere new. Below ~3s the cache expires *between*
// bursts, which is the worst of both worlds (we pay full cost on
// the burst's first call, every time). Above ~5s we start serving
// noticeably stale messages while the agent is mid-stream.
//
// Real-time updates for the *currently-viewed* session still come
// through the SSE event stream, which doesn't go through this
// cache. So the cache only affects refreshes triggered by route
// transitions, focus events, etc. — exactly the cases where 5s of
// staleness is invisible. Failures are not cached (see httpcache.go).
var sessionCache = newHTTPCacheNamed(5*time.Second, "opencode.session_http")

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

// getJSONCached is getJSON wrapped through catalogCache. Callers that
// fetch effectively-immutable catalog data should use this; one-shot
// reads of session-specific data must keep using getJSON.
//
// On a hit, no HTTP call is made. On a miss the underlying getJSON
// runs (singleflighted across concurrent callers), and a successful
// 200/JSON response is cached for catalogCache's TTL. Failures are
// not cached — see httpCache.getOrFetch.
func getJSONCached(ctx context.Context, port, path string) ([]byte, bool) {
	return catalogCache.getOrFetch(port, path, func() ([]byte, bool) {
		return getJSON(ctx, port, path)
	})
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
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
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
		if resp.StatusCode < 500 {
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
