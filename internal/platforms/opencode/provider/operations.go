package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv1"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// This file holds the Platform-interface operation methods that were
// formerly inlined in internal/server. Each method resolves the running
// OpenCode port from the native session binding and proxies the
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
	s, err := a.db.GetSession(ctx, sessionID)
	dbPhase.End()
	if err != nil {
		return "", nil, platforms.ErrNotFound
	}
	port, err = a.rt.resolveSessionPortCtx(ctx, sessionID, s.Directory)
	if err != nil {
		return "", s, err
	}
	if port == "" {
		return "", s, fmt.Errorf("no running OpenCode instance for session %s: %w", sessionID, platforms.ErrPlatformUnreachable)
	}
	a.rt.rememberSessionPort(sessionID, port)
	return port, s, nil
}

func (d *Discovery) resolveOpenCodePortBySession(ctx context.Context, sessionID string) (string, error) {
	for _, server := range d.discoverOpenCodeServers() {
		if server.port == "" {
			continue
		}
		found, err := probeSessionPort(ctx, server.port, sessionID)
		if err != nil {
			return "", err
		}
		if found {
			return server.port, nil
		}
	}
	return "", nil
}

// --- Catalogs ---

// ProjectCatalog reads catalogs from a managed project before it has a session.
// directory scopes the read on an OpenCode v2 server, which serves every
// project; "" reads the server's default location.
func ProjectCatalog(ctx context.Context, endpoint, directory string) (agents, models []string, err error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Port() == "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, nil, errors.New("invalid OpenCode endpoint")
	}
	body, err := getJSONCached(ctx, u.Port(), scopedPath(ctx, u.Port(), "/agent", directory))
	if err != nil {
		return nil, nil, err
	}
	var rawAgents []map[string]interface{}
	if err := json.Unmarshal(body, &rawAgents); err != nil {
		return nil, nil, err
	}
	for _, agent := range rawAgents {
		if name := stringField(agent, "name"); name != "" {
			agents = append(agents, name)
		}
	}
	body, err = getJSONCached(ctx, u.Port(), scopedPath(ctx, u.Port(), "/provider", directory))
	if err != nil {
		return nil, nil, err
	}
	var providers OpenCodeProvidersResponse
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, nil, err
	}
	// Only connected providers: "all" is the whole models.dev catalog (thousands
	// of models), and unusable providers like 302ai sort ahead of anthropic.
	for _, provider := range providers.All {
		if !slices.Contains(providers.Connected, provider.ID) {
			continue
		}
		for model := range provider.Models {
			models = append(models, provider.ID+"/"+model)
		}
	}
	sort.Strings(agents)
	sort.Strings(models)
	return agents, models, nil
}

// agentCatalogAt reads /agent from one instance. Failures return nil so
// the frontend keeps rendering an empty catalog.
func agentCatalogAt(ctx context.Context, port, sessionID, directory string) []platforms.AgentCatalogEntry {
	body, fetchErr := getJSONCached(ctx, port, scopedPath(ctx, port, "/agent", directory))
	if fetchErr != nil {
		logFetchFailure(fetchErr, log.Fields{"sessionID": sessionID, "port": port, "endpoint": "/agent"},
			"opencode: agent catalog fetch failed; returning empty list")
		return nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "error": err}).
			Warn("opencode: agent catalog decode failed; returning empty list")
		return nil
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
	return entries
}

// slashCommandsAt reads /command from one instance; failures return nil.
func slashCommandsAt(ctx context.Context, port, sessionID, directory string) []platforms.SlashCommandEntry {
	body, fetchErr := getJSONCached(ctx, port, scopedPath(ctx, port, "/command", directory))
	if fetchErr != nil {
		logFetchFailure(fetchErr, log.Fields{"sessionID": sessionID, "port": port, "endpoint": "/command"},
			"opencode: slash commands fetch failed; returning empty list")
		return nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "error": err}).
			Warn("opencode: slash commands decode failed; returning empty list")
		return nil
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
	return entries
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

// SendMessage submits a composer message via POST /session/{id}/prompt_async.
// On an OpenCode v2 server the ocv2 transport translates it to v2's prompt
// API (agent/model switches, delivery).
func (a *Adapter) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	lockIndex := crc32.ChecksumIEEE([]byte(req.SessionID)) % uint32(len(a.sendLock))
	sendMu := &a.sendLock[lockIndex]
	sendMu.Lock()
	defer sendMu.Unlock()

	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	err = sendMessageOnPort(ctx, port, req)
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
		if retryErr := sendMessageOnPort(ctx, retryPort, req); retryErr == nil {
			a.rt.rememberSessionPort(req.SessionID, retryPort)
			return nil
		}
	}
	return err
}

func sendMessageOnPort(ctx context.Context, port string, req platforms.SendMessageRequest) error {
	// Only an OpenCode v2 server holds follow-ups itself (its session
	// inbox); everything else uses ocman's queue.
	if req.Delivery != "" && !ocv2.IsV2(ctx, port) {
		return platforms.ErrUnsupported
	}
	err := sendMessageLegacy(ctx, port, req)
	// The v2 translation refuses (412) a held prompt that needs an agent
	// or model switch: nothing was admitted, so ocman's queue may take it.
	var upstream *platforms.UpstreamError
	if req.Delivery != "" && errors.As(err, &upstream) && upstream.Status == http.StatusPreconditionFailed {
		return platforms.ErrUnsupported
	}
	return err
}

func sendMessageLegacy(ctx context.Context, port string, req platforms.SendMessageRequest) error {
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
		body["delivery"] = req.Delivery // read by the ocv2 translation only
	}
	payload, err := marshalRequest(body)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/session/%s/prompt_async", req.SessionID)
	if err := postJSON(ctx, port, path, payload); err != nil {
		return err
	}
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
// running managed instance to resolve).
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
	// Slash commands run through the model; the request context bounds
	// the wait, not the 10s read client.
	return postJSONLLM(ctx, port, fmt.Sprintf("/session/%s/command", req.SessionID), payload)
}

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
		err = postJSON(ctx, port, path, payload)
	} else {
		err = postJSONForDirectory(ctx, port, path, directory, payload)
	}
	if err != nil && !isUpstreamNotFound(err) {
		return err
	}
	// A 404 means OpenCode already dropped the prompt (answered elsewhere,
	// or the turn was aborted — which fires no permission.replied). Forget
	// it too, otherwise the UI keeps resurrecting a prompt nobody can answer.
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
	err = postJSONForDirectory(ctx, port, fmt.Sprintf("/question/%s/reply", req.RequestID), directory, payload)
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
	err = postJSONForDirectory(ctx, port, fmt.Sprintf("/question/%s/reject", req.RequestID), directory, []byte("{}"))
	if err != nil && !isUpstreamNotFound(err) {
		return err
	}
	a.ObservePromptResolved(directory, "question", promptSessionID, req.RequestID)
	return err
}

// Abort cancels the in-flight response for a session.
func (a *Adapter) Abort(ctx context.Context, req platforms.AbortRequest) error {
	if req.TaskID != "" {
		return a.stopBackgroundTask(ctx, req)
	}
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	return postJSON(ctx, port, fmt.Sprintf("/session/%s/abort", req.SessionID), []byte("{}"))
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
	if err := sendJSON(ctx, http.MethodDelete, port, path, nil); err != nil && !isUpstreamNotFound(err) {
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
	if err := postJSON(ctx, port, fmt.Sprintf("/session/%s/revert", req.SessionID), payload); err != nil {
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
	if err := postJSON(ctx, port, fmt.Sprintf("/session/%s/unrevert", req.SessionID), []byte("{}")); err != nil {
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
	// Uncached: the toggle UI needs read-after-write freshness.
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
	// Summarizing the history is itself a model call.
	return postJSONLLM(ctx, port, fmt.Sprintf("/session/%s/summarize", req.SessionID), payload)
}

// ForkSession branches a session into a new child session via
// OpenCode's POST /session/{id}/fork, optionally from a specific
// message. Returns the new session's ID.
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
	body, err := postJSONReturning(ctx, port, fmt.Sprintf("/session/%s/fork", req.SessionID), payload)
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

// MoveSession relocates a session to another project directory via
// OpenCode's POST /experimental/control-plane/move-session.
func (a *Adapter) MoveSession(ctx context.Context, req platforms.MoveSessionRequest) error {
	port, _, err := a.resolvePort(req.SessionID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"sessionID":   req.SessionID,
		"destination": map[string]string{"directory": req.Directory},
	})
	if err != nil {
		return err
	}
	if err := postJSON(ctx, port, "/experimental/control-plane/move-session", payload); err != nil {
		return err
	}
	return nil
}

// CreateSession creates a new OpenCode session bound to the given
// directory. Returns the new session ID.
func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	// When the caller already knows the instance's port (e.g. a
	// worktree session created on the project's single instance),
	// skip the managed inventory lookup entirely.
	port := req.Port
	if port == "" {
		// Fast path: reuse the cached port map when opencode is already
		// running (the common case). Only fall back to a fresh uncached
		// inventory refresh on a miss to catch a just-launched runtime.
		portPhase := srvtiming.Begin(ctx, "runtime_ports_fresh")
		port = a.rt.discoverOpenCodePort(req.Directory)
		if port == "" {
			port = a.rt.discoverOpenCodePortFresh(req.Directory)
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
			"discovered_dirs": a.rt.discoveredOpenCodeDirs(),
		}).Warn("no running OpenCode instance for directory")
		return nil, fmt.Errorf("no running OpenCode instance for directory %s: %w", req.Directory, platforms.ErrPlatformUnreachable)
	}

	createPhase := srvtiming.Begin(ctx, "http_create")
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session", port)
	create := map[string]string{}
	if req.NativeID != "" {
		create["id"] = req.NativeID
	}
	if req.Title != "" {
		// OpenCode accepts the title at creation; no follow-up PATCH.
		create["title"] = req.Title
	}
	createBody, err := marshalRequest(create)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(createBody))
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
	// Session creation may outlive the shared API timeout; the caller context still bounds it.
	resp, err := openCodeLLMClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("opencode create-session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("opencode create-session: upstream HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	body, readErr := apiv1.ReadLimited(resp.Body, maxUpstreamErrorBytes)
	if readErr != nil {
		return nil, fmt.Errorf("opencode create-session: %w", readErr)
	}
	// A session without an ID is unusable — the caller can never address
	// it. Fail loudly rather than returning a "created" session with an
	// empty ID, surfacing the raw body so the cause is visible.
	if err := json.Unmarshal(body, &parsed); err != nil {
		if len(body) == 0 {
			return nil, errors.New("opencode create-session: empty response")
		}
		return nil, fmt.Errorf("opencode create-session: unparseable response: %s", strings.TrimSpace(string(body)))
	}
	if parsed.ID == "" {
		return nil, fmt.Errorf("opencode create-session: response carried no session id: %s", strings.TrimSpace(string(body)))
	}
	createPhase.EndWithDesc("opencode POST /session")

	// Older OpenCode builds ignored the create-time title; fall back to a PATCH.
	if req.Title != "" && parsed.Title != req.Title {
		if err := setSessionTitle(ctx, port, parsed.ID, req.Title); err != nil {
			log.WithError(err).Warn("failed to set custom title on new session")
			// Don't fail the entire creation if title setting fails.
		}
	}
	a.rt.rememberSessionPort(parsed.ID, port)

	return &platforms.CreateSessionResponse{ID: parsed.ID}, nil
}

// setSessionTitle renames a session on a known OpenCode port.
func setSessionTitle(ctx context.Context, port, sessionID, title string) error {
	payload, err := marshalRequest(map[string]string{"title": title})
	if err != nil {
		return err
	}
	if err := patchJSON(ctx, port, "/session/"+url.PathEscape(sessionID), payload); err != nil {
		return err
	}
	return nil
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
