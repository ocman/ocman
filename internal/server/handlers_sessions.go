package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

const maxComposerAttachmentBytes = 100 << 20

// --- Sessions aggregation ---

// handleSessions fans out to every registered Platform adapter for
// session data, then applies local state (archived / seen).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	since := parseInt64Param(r, "since", 0)
	limit := parseIntParam(r, "limit", 500)

	ctx := r.Context()
	// Concurrent, non-blocking fan-out across local + remote adapters.
	// Remote adapters are bounded by a short timeout so a slow/offline
	// remote never delays the unified list (FR-15, NFR-1).
	fanPhase := srvtiming.Begin(ctx, "sessions_fanout")
	all := s.fanOutSessions(ctx, dir, since, s.registry.RememberSessions)
	fanPhase.End()

	// Force-include pinned sessions that fell outside the time window.
	// The pinned set is typically <10 entries; each miss is a single
	// adapter lookup. Silently skip sessions that are deleted or
	// inaccessible.
	if pinned, err := s.stateDB.PinnedSessions(); err == nil && len(pinned) > 0 {
		have := make(map[state.Key]bool, len(all))
		for _, sess := range all {
			have[state.Key{Platform: sess.Platform, SessionID: sess.ID}] = true
		}
		for key := range pinned {
			if have[key] {
				continue
			}
			adapter, ok := s.registry.Get(platforms.ID(key.Platform))
			if !ok || !adapter.Available(ctx) {
				continue
			}
			detail, err := adapter.Session(ctx, key.SessionID, 0, 0)
			if err != nil || detail == nil || detail.Session == nil {
				continue
			}
			all = append(all, *detail.Session)
		}
	}

	// Sort all platforms together by recency, then apply the limit.
	all = sortAndLimitSessions(all, limit)

	statePhase := srvtiming.Begin(ctx, "state_overlay")
	err := s.applySessionState(all)
	statePhase.EndWithDesc("applySessionState")
	if err != nil {
		serverError(w, "fetching session state", err)
		return
	}

	// Enrich errored sessions with normalized notices (e.g. rate-limit
	// backoff) so the frontend can surface the reason without
	// platform-specific parsing.
	applySessionNotice(all)

	// Note: git status info is no longer attached here. The
	// /api/sessions handler used to fan out up to 8 concurrent
	// `git status` subprocesses per request, which produced
	// fork-pressure pauses on macOS (multi-second hiccups across
	// unrelated handlers; see docs/profiling.md). Components that
	// need per-directory git state now request /api/git/info
	// explicitly while they're mounted, so subprocess work is
	// scoped to "the user is actually looking at this directory"
	// rather than "every dashboard poll, every 5 seconds".

	writeJSON(w, all)
}

// notifyEntry is a minimal per-session payload used by the favicon/title
// notification poller and the in-app toast notifier. Keeping the
// response small reduces bandwidth and lets the poller query a longer
// time-window (e.g. 500 sessions) without the cost of a full
// /api/sessions payload.
//
// Title and Directory are included so the toast notifier can render a
// useful "session needs input" message ("Refactor auth (/repo/foo)")
// with a deep link, without a second round-trip.
type notifyEntry struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Seen              bool   `json:"seen"`
	PendingPermission bool   `json:"pendingPermission,omitempty"`
	PendingQuestion   bool   `json:"pendingQuestion,omitempty"`
	Title             string `json:"title,omitempty"`
	Directory         string `json:"directory,omitempty"`
}

// handleSessionsNotify returns a minimal projection of the sessions
// list used by the client's favicon/title notification logic. Only
// sessions that could contribute to the notification state are
// returned:
//
//   - any session with a pending permission or question prompt
//   - sessions whose status is "waiting" or "error" and that haven't
//     been seen
//
// Everything else is filtered out server-side so the response stays
// tiny even with a large time window.
func (s *Server) handleSessionsNotify(w http.ResponseWriter, r *http.Request) {
	since := parseInt64Param(r, "since", 0)
	limit := parseIntParam(r, "limit", 500)

	ctx := r.Context()
	all := s.fanOutSessions(ctx, "", since, nil)

	all = sortAndLimitSessions(all, limit)

	if err := s.applySessionState(all); err != nil {
		serverError(w, "fetching session state for notify", err)
		return
	}

	// Project + filter. Only keep sessions that could drive the UI.
	out := make([]notifyEntry, 0, len(all))
	for i := range all {
		se := &all[i]
		hasPrompt := se.PendingPermission || se.PendingQuestion
		isUnseenTerminal := (se.Status == "waiting" || se.Status == "error") && !se.Seen
		if !hasPrompt && !isUnseenTerminal {
			continue
		}
		out = append(out, notifyEntry{
			ID:                se.ID,
			Status:            se.Status,
			Seen:              se.Seen,
			PendingPermission: se.PendingPermission,
			PendingQuestion:   se.PendingQuestion,
			Title:             se.Title,
			Directory:         se.Directory,
		})
	}

	writeJSON(w, out)
}

// --- Session detail ---

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session/")

	limit := parseIntParam(r, "limit", 30)
	offset := parseIntParam(r, "offset", 0)

	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}

	detail, err := adapter.Session(r.Context(), sessionID, limit, offset)
	if err != nil {
		writePlatformError(w, "fetching session", err)
		return
	}
	// `nil` slices would marshal as `null`; the frontend expects
	// `[]` for empty messages/parts so the useState reducers can
	// diff cheaply.
	if detail.Messages == nil {
		detail.Messages = []db.Message{}
	}
	if detail.Parts == nil {
		detail.Parts = []db.Part{}
	}
	// Enrich the session with a normalized notice (e.g. rate-limit).
	if detail.Session != nil {
		detail.Session.Notice = deriveSessionNotice(*detail.Session)
	}

	// Opening a session unarchives it (and its project) so the sidebar
	// shows the project + session tile again and navigation stays
	// consistent. The user can re-archive from the sidebar. Skipped for
	// remote sessions (AD-14b): their archive state lives in the remote's
	// state.db, not the hub's.
	if s.stateDB != nil && detail.Session != nil && !isRemotePlatformID(string(adapter.ID())) {
		if err := s.stateDB.UnarchiveSession(string(adapter.ID()), sessionID); err != nil {
			log.Printf("unarchiving session on open: %v", err)
		}
		if err := s.stateDB.UnarchiveProject(projectRootForDirectory(detail.Session.Directory)); err != nil {
			log.Printf("unarchiving project on open: %v", err)
		}
	}

	// Inject persisted auto-approve notice messages/parts so they
	// arrive pre-sorted with the real messages. The frontend never
	// needs a separate fetch or client-side injection; the notices
	// land in chronological order alongside the real conversation.
	//
	// Skipped for remote sessions (AD-14b): their approval decisions and
	// notice records live in the REMOTE's state.db, and the remote's
	// Session RPC already returns detail enriched with its own notices.
	// Injecting from the hub's DB here would read the wrong store.
	if s.stateDB != nil && !isRemotePlatformID(string(adapter.ID())) {
		injectApprovalNotices(string(adapter.ID()), sessionID, s.stateDB, &detail.Messages, &detail.Parts)
	}

	writeJSON(w, map[string]interface{}{
		"session":           detail.Session,
		"messages":          detail.Messages,
		"parts":             detail.Parts,
		"totalMessages":     detail.TotalMessages,
		"contextTokenCount": detail.ContextTokenCount,
		"defaultAgent":      detail.DefaultAgent,
		"defaultModel":      detail.DefaultModel,
	})
}

// injectApprovalNotices fetches persisted auto-approve records from
// state and inserts synthetic notice messages/parts into msgs and parts,
// keeping both slices sorted by timeCreated ascending. Existing notice
// messages (identified by their "ocman-notice-" prefix) are skipped so
// repeated calls are idempotent.
func injectApprovalNotices(platform, sessionID string, stateDB interface {
	ListApprovedPermissions(platform, sessionID string) ([]state.ApprovedPermission, error)
}, msgs *[]db.Message, parts *[]db.Part) {
	approved, err := stateDB.ListApprovedPermissions(platform, sessionID)
	if err != nil || len(approved) == 0 {
		return
	}

	// Build a set of notice IDs already present so we never double-inject.
	existing := make(map[string]bool, len(*msgs))
	for _, m := range *msgs {
		existing[m.ID] = true
	}

	for _, p := range approved {
		// Stable key uses the OpenCode permission ID, which is guaranteed
		// to be unique per approval. Legacy rows (written before the
		// judge session was deleted post-verdict) populated this with the
		// judge session ID instead — we fall back to that only when
		// permission_id is empty, which should never happen for any
		// row produced by RecordApprovedPermission.
		keyPart := p.PermissionID
		if keyPart == "" {
			keyPart = p.JudgeSessionID
		}
		stableKey := "ocman-notice-" + keyPart
		if existing[stableKey] {
			continue
		}
		existing[stableKey] = true

		patterns := p.Patterns
		if patterns == nil {
			patterns = []string{}
		}
		partData, _ := json.Marshal(map[string]interface{}{
			"type":       "auto-approved",
			"permission": p.PermissionText,
			"patterns":   patterns,
			"reasoning":  p.Reasoning,
		})
		ts := p.ApprovedAt

		noticeMsg := db.Message{
			ID:          stableKey,
			SessionID:   sessionID,
			TimeCreated: ts,
			Data:        json.RawMessage(`{"role":"notice"}`),
		}
		noticePart := db.Part{
			ID:          stableKey + "-part",
			MessageID:   stableKey,
			SessionID:   sessionID,
			TimeCreated: ts,
			Data:        json.RawMessage(partData),
		}

		// Insert the message in chronological order.
		inserted := false
		for i, m := range *msgs {
			if m.TimeCreated > ts {
				newMsgs := make([]db.Message, 0, len(*msgs)+1)
				newMsgs = append(newMsgs, (*msgs)[:i]...)
				newMsgs = append(newMsgs, noticeMsg)
				newMsgs = append(newMsgs, (*msgs)[i:]...)
				*msgs = newMsgs
				inserted = true
				break
			}
		}
		if !inserted {
			*msgs = append(*msgs, noticeMsg)
		}

		// Insert the part in chronological order (parts are matched by
		// messageId at render time, so order here just keeps the slice tidy).
		partInserted := false
		for i, pt := range *parts {
			if pt.TimeCreated > ts {
				newParts := make([]db.Part, 0, len(*parts)+1)
				newParts = append(newParts, (*parts)[:i]...)
				newParts = append(newParts, noticePart)
				newParts = append(newParts, (*parts)[i:]...)
				*parts = newParts
				partInserted = true
				break
			}
		}
		if !partInserted {
			*parts = append(*parts, noticePart)
		}
	}
}

// handleSessionTasks returns sub-session data for a batch of task
// sessions. The frontend uses this to render embedded thread previews
// inside Task tool cards and to show live streaming output while a
// subagent is still running.
//
// Query params:
//   - ids: comma-separated list of task session IDs.
//   - limit: max messages per sub-session (default 10, max 30).
//
// Response: { "tasks": { "<taskId>": { "messages": [...], "parts": [...] } } }
func (s *Server) handleSessionTasks(w http.ResponseWriter, r *http.Request) {
	idsParam := r.URL.Query().Get("ids")
	if idsParam == "" {
		writeJSON(w, map[string]interface{}{"tasks": map[string]interface{}{}})
		return
	}

	ids := strings.Split(idsParam, ",")
	const maxBatch = 20
	if len(ids) > maxBatch {
		ids = ids[:maxBatch]
	}

	limit := parseIntParam(r, "limit", 10)
	if limit < 1 {
		limit = 1
	}
	if limit > 30 {
		limit = 30
	}

	type taskData struct {
		Messages json.RawMessage `json:"messages"`
		Parts    json.RawMessage `json:"parts"`
	}
	result := make(map[string]taskData, len(ids))
	for _, taskID := range ids {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}

		adapter, ok := s.registry.PlatformForSession(r.Context(), taskID)
		if !ok {
			continue
		}

		detail, err := adapter.Session(r.Context(), taskID, limit, 0)
		if err != nil {
			continue
		}

		msgs := detail.Messages
		if msgs == nil {
			msgs = []db.Message{}
		}
		pts := detail.Parts
		if pts == nil {
			pts = []db.Part{}
		}

		msgsJSON, err := json.Marshal(msgs)
		if err != nil {
			continue
		}
		ptsJSON, err := json.Marshal(pts)
		if err != nil {
			continue
		}

		result[taskID] = taskData{Messages: msgsJSON, Parts: ptsJSON}
	}

	writeJSON(w, map[string]interface{}{"tasks": result})
}

// --- Session-scoped read endpoints ---

// listViaAdapter collapses the shared shape of the session-scoped list
// endpoints (agents, commands, questions): resolve the adapter, call
// one list method, nil-guard to an empty JSON array, write the result.
// A free function because Go methods can't have type parameters.
func listViaAdapter[T any](s *Server, w http.ResponseWriter, r *http.Request, errContext string, fetch func(platforms.Platform, context.Context, string) ([]T, error)) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := fetch(adapter, r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, errContext, err)
			return
		}
		if entries == nil {
			entries = []T{}
		}
		writeJSON(w, entries)
	})
}

func (s *Server) handleSessionAgents(w http.ResponseWriter, r *http.Request) {
	listViaAdapter(s, w, r, "fetching agent catalog", platforms.Platform.AgentCatalog)
}

func (s *Server) handleSessionCommands(w http.ResponseWriter, r *http.Request) {
	listViaAdapter(s, w, r, "fetching slash commands", platforms.Platform.SlashCommands)
}

func (s *Server) handleSessionModels(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		resp, err := adapter.SessionModels(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "fetching session models", err)
			return
		}
		if resp == nil {
			resp = &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}
		}
		writeJSON(w, resp)
	})
}

func (s *Server) handleSessionPermissions(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := adapter.ListPermissions(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "listing permissions", err)
			return
		}
		if entries == nil {
			entries = []platforms.LivePrompt{}
		}
		// Kick off auto-approve for any pending permissions resurrected
		// via REST. Without this, prompts that exist before the SSE
		// stream connects (page reload, navigation to a session that
		// already has a pending permission) would never trigger the
		// judge, leaving the UI stuck on the prompt indefinitely.
		// ensureAutoApprove deduplicates against the SSE tee so we
		// don't double-judge a permission that arrives via both paths.
		for _, entry := range entries {
			permissionID, _ := entry["id"].(string)
			permission, _ := entry["permission"].(string)
			if permissionID == "" || permission == "" {
				continue
			}
			patterns := extractPermissionPatterns(entry)
			metadata := extractPermissionMetadata(entry)
			s.ensureAutoApprove(adapter.ID(), adapter, sessionID, permissionID, permission, patterns, metadata)
		}
		writeJSON(w, entries)
	})
}

// extractPermissionPatterns reads the "patterns" array from a
// LivePrompt map, tolerating both []string (rare) and []interface{}
// (the default after json.Unmarshal into a generic map).
func extractPermissionPatterns(entry platforms.LivePrompt) []string {
	raw, ok := entry["patterns"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// extractPermissionMetadata reads the "metadata" object from a
// LivePrompt map. Returns nil when absent or not an object — the
// judge prompt formatter handles nil cleanly (no metadata block
// appears in the prompt).
func extractPermissionMetadata(entry platforms.LivePrompt) map[string]any {
	raw, ok := entry["metadata"]
	if !ok {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return nil
}

func (s *Server) handleSessionQuestions(w http.ResponseWriter, r *http.Request) {
	listViaAdapter(s, w, r, "listing questions", platforms.Platform.ListQuestions)
}

// handleSessionChanges aggregates every file-touching tool call in a
// session into a per-file changes summary. Adapters that don't support
// the operation (Claude Code) are surfaced as a Supported=false payload
// rather than an HTTP error so the frontend has a single shape to render.
func (s *Server) handleSessionChanges(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		changes, err := adapter.SessionChanges(r.Context(), sessionID)
		if err != nil {
			if errors.Is(err, platforms.ErrUnsupported) {
				writeJSON(w, &platforms.SessionChanges{SessionID: sessionID, Files: []platforms.FileChange{}})
				return
			}
			writePlatformError(w, "fetching session changes", err)
			return
		}
		if changes == nil {
			changes = &platforms.SessionChanges{SessionID: sessionID, Files: []platforms.FileChange{}}
		}
		if changes.Files == nil {
			changes.Files = []platforms.FileChange{}
		}
		writeJSON(w, changes)
	})
}

// handleSessionInfo returns the per-session info snapshot consumed by
// the right-hand "Session info" panel.
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		info, err := adapter.SessionInfo(r.Context(), sessionID)
		if err != nil {
			if errors.Is(err, platforms.ErrUnsupported) {
				writeJSON(w, &platforms.SessionInfo{SessionID: sessionID, MCPServers: []platforms.MCPServer{}, LSPServers: []platforms.LSPServer{}})
				return
			}
			writePlatformError(w, "fetching session info", err)
			return
		}
		if info == nil {
			info = &platforms.SessionInfo{SessionID: sessionID, MCPServers: []platforms.MCPServer{}, LSPServers: []platforms.LSPServer{}}
		}
		if info.MCPServers == nil {
			info.MCPServers = []platforms.MCPServer{}
		}
		if info.LSPServers == nil {
			info.LSPServers = []platforms.LSPServer{}
		}
		writeJSON(w, info)
	})
}

// --- Session-scoped mutating endpoints ---

func (s *Server) handleSessionMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
		Images  []struct {
			URL  string `json:"url"`
			Mime string `json:"mime"`
		} `json:"images"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Reasoning string `json:"reasoning"`
	}
	if !readAndUnmarshal(w, r, maxSendMessageBody, &req) {
		return
	}
	if req.Message == "" && len(req.Images) == 0 {
		http.Error(w, "message or images required", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		images := make([]platforms.ImageAttachment, 0, len(req.Images))
		for _, img := range req.Images {
			images = append(images, platforms.ImageAttachment{URL: img.URL, Mime: img.Mime})
		}
		if err := adapter.SendMessage(r.Context(), platforms.SendMessageRequest{
			SessionID: sessionID,
			Message:   req.Message,
			Images:    images,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
		}); err != nil {
			writePlatformError(w, "sending message", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionAttachment(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		r.Body = http.MaxBytesReader(w, r.Body, maxComposerAttachmentBytes)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, "failed to read attachment", http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			writePlatformError(w, "loading session for attachment", err)
			return
		}
		if detail == nil || detail.Session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		root, err := composerAttachmentDir(detail.Session.Directory, sessionID)
		if err != nil {
			log.WithError(err).Error("resolving composer attachment directory")
			http.Error(w, "failed to prepare attachment directory", http.StatusInternalServerError)
			return
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			log.WithError(err).Error("creating composer attachment directory")
			http.Error(w, "failed to prepare attachment directory", http.StatusInternalServerError)
			return
		}

		name := safeAttachmentName(header.Filename)
		path := filepath.Join(root, fmt.Sprintf("%d-%s", time.Now().UnixNano(), name))
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			log.WithError(err).Error("creating composer attachment file")
			http.Error(w, "failed to save attachment", http.StatusInternalServerError)
			return
		}
		size, copyErr := io.Copy(out, file)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(path)
			if copyErr != nil {
				log.WithError(copyErr).Error("saving composer attachment")
			} else {
				log.WithError(closeErr).Error("closing composer attachment")
			}
			http.Error(w, "failed to save attachment", http.StatusInternalServerError)
			return
		}

		mime := header.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		writeJSON(w, map[string]interface{}{
			"path": path,
			"name": name,
			"mime": mime,
			"size": size,
		})
	})
}

func composerAttachmentDir(projectDir, sessionID string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	projectKey := strconv.FormatUint(fnv64(projectDir), 36)
	return filepath.Join(base, "ocman", "composer-attachments", projectKey, safeAttachmentName(sessionID)), nil
}

func safeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "attachment"
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "attachment"
	}
	return b.String()
}

func fnv64(s string) uint64 {
	const prime uint64 = 1099511628211
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func (s *Server) handleSessionCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command   string `json:"command"`
		Arguments string `json:"arguments"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Reasoning string `json:"reasoning"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := adapter.ExecuteCommand(r.Context(), platforms.ExecuteCommandRequest{
			SessionID: sessionID,
			Command:   req.Command,
			Arguments: req.Arguments,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
		}); err != nil {
			writePlatformError(w, "executing command", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionRestartOpencode(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			writePlatformError(w, "loading session", err)
			return
		}
		if detail == nil || detail.Session == nil || detail.Session.Directory == "" {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		target, err := restartOpencodeInTmux(detail.Session.Directory)
		if err != nil {
			if errors.Is(err, errNoManagedOpencodePane) {
				http.Error(w, "no tmux-managed OpenCode pane found for this session", http.StatusConflict)
				return
			}
			serverError(w, "restarting opencode", err)
			return
		}
		writeJSON(w, map[string]string{"target": target})
	})
}

// handleSessionShell handles POST /api/session/{id}/shell — runs a
// raw shell command in the session's working directory, bypassing the LLM.
func (s *Server) handleSessionShell(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
		Agent   string `json:"agent"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := adapter.RunShell(r.Context(), platforms.RunShellRequest{
			SessionID: sessionID,
			Command:   req.Command,
			Agent:     req.Agent,
		}); err != nil {
			writePlatformError(w, "running shell command", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := adapter.RenameSession(r.Context(), platforms.RenameSessionRequest{
			SessionID: sessionID,
			Title:     req.Title,
		}); err != nil {
			writePlatformError(w, "renaming session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionAbort(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := adapter.Abort(r.Context(), platforms.AbortRequest{SessionID: sessionID}); err != nil {
			writePlatformError(w, "aborting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.ProviderID == "" || req.ModelID == "" {
		http.Error(w, "providerID and modelID are required", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := adapter.Compact(r.Context(), platforms.CompactRequest{
			SessionID:  sessionID,
			ProviderID: req.ProviderID,
			ModelID:    req.ModelID,
		}); err != nil {
			writePlatformError(w, "compacting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionPermissionRulesGet handles GET /api/session/{id}/permission-rules.
// Returns {"rules":[...]}; an empty list means the session inherits the
// platform's configured defaults.
func (s *Server) handleSessionPermissionRulesGet(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		rules, err := adapter.PermissionRules(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "reading permission rules", err)
			return
		}
		if rules == nil {
			rules = []platforms.PermissionRule{}
		}
		writeJSON(w, map[string]interface{}{"rules": rules})
	})
}

// handleSessionPermissionRulesSet handles PUT /api/session/{id}/permission-rules.
// Body: {"rules":[{"permission","pattern","action"}...]}. An empty list
// restores the platform's configured defaults.
func (s *Server) handleSessionPermissionRulesSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []platforms.PermissionRule `json:"rules"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if len(req.Rules) > 100 {
		http.Error(w, "too many rules", http.StatusBadRequest)
		return
	}
	for i := range req.Rules {
		rule := &req.Rules[i]
		if rule.Permission == "" {
			http.Error(w, "rule permission is required", http.StatusBadRequest)
			return
		}
		if rule.Pattern == "" {
			rule.Pattern = "*"
		}
		switch rule.Action {
		case "allow", "deny", "ask":
		default:
			http.Error(w, "invalid rule action: expected allow, deny, or ask", http.StatusBadRequest)
			return
		}
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := adapter.SetPermissionRules(r.Context(), platforms.SetPermissionRulesRequest{
			SessionID: sessionID,
			Rules:     req.Rules,
		}); err != nil {
			writePlatformError(w, "setting permission rules", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionPermission handles POST /api/session/{id}/permissions/{pid}
func (s *Server) handleSessionPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reply string `json:"reply"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	switch req.Reply {
	case "once", "always", "reject":
	default:
		http.Error(w, "invalid reply value: expected once, always, or reject", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string, adapter platforms.Platform) {
		permissionID := strings.TrimPrefix(rest, "permissions/")
		if !validateID(permissionID) {
			http.Error(w, "invalid permission ID", http.StatusBadRequest)
			return
		}
		// Cancel any in-flight auto-approve judge before we forward the
		// reply: the user has decided, so we must not race their answer
		// with the AI's verdict, and we must not pay for a judge whose
		// result will be discarded anyway. cancelAutoApprove is safe to
		// call when no judge is running (returns false; we don't care).
		s.cancelAutoApprove(sessionID, permissionID)
		if err := adapter.RespondPermission(r.Context(), platforms.RespondPermissionRequest{
			SessionID:    sessionID,
			PermissionID: permissionID,
			Reply:        req.Reply,
		}); err != nil {
			writePlatformError(w, "responding to permission", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionQuestion dispatches POST /api/session/{id}/questions/{qid}
// and POST /api/session/{id}/questions/{qid}/reject.
func (s *Server) handleSessionQuestion(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string, adapter platforms.Platform) {
		rest = strings.TrimPrefix(rest, "questions/")
		questionID := rest
		reject := false
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			questionID = rest[:slash]
			if rest[slash+1:] == "reject" {
				reject = true
			} else {
				http.Error(w, "unknown question subpath", http.StatusNotFound)
				return
			}
		}
		if !validateID(questionID) {
			http.Error(w, "invalid question ID", http.StatusBadRequest)
			return
		}
		if reject {
			if err := adapter.RejectQuestion(r.Context(), platforms.RejectQuestionRequest{
				SessionID: sessionID,
				RequestID: questionID,
			}); err != nil {
				writePlatformError(w, "rejecting question", err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			Answers [][]string `json:"answers"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &req) {
			return
		}
		if err := adapter.RespondQuestion(r.Context(), platforms.RespondQuestionRequest{
			SessionID: sessionID,
			RequestID: questionID,
			Answers:   req.Answers,
		}); err != nil {
			writePlatformError(w, "responding to question", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// --- Auto-approve endpoints ---

// handleSessionAutoApproveGet handles GET /api/session/{id}/auto-approve.
// Returns the effective auto-approve state for the session: the per-session
// override when set, otherwise the server-wide default.
func (s *Server) handleSessionAutoApproveGet(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		enabled, exists, err := s.stateDB.GetAutoApprove(string(adapter.ID()), sessionID)
		if err != nil {
			serverError(w, "getting auto-approve state", err)
			return
		}
		effective := s.autoApproveDefault
		if exists {
			effective = enabled
		}
		writeJSON(w, map[string]interface{}{
			"enabled":    effective,
			"overridden": exists,
		})
	})
}

// handleSessionAutoApproveSet handles POST /api/session/{id}/auto-approve.
// Body: {"enabled": true|false}
//
// When toggling from disabled to enabled, the handler additionally
// kicks off the auto-approve judge for any *already-pending* permission
// in this session. Without this, a user who sees a permission prompt
// and only then clicks "Enable auto-approve" would be stuck on the
// "starting…" UI forever: the original permission.asked event already
// fired (and was discarded because auto-approve was off at the time),
// no new event ever arrives for the same prompt, and the frontend
// doesn't refetch permissions on toggle. See
// PermissionPrompt.tsx fallback at "Auto-approve on · starting…".
func (s *Server) handleSessionAutoApproveSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := s.stateDB.SetAutoApprove(string(adapter.ID()), sessionID, req.Enabled); err != nil {
			serverError(w, "setting auto-approve state", err)
			return
		}
		// When enabling, resurrect any pending permissions so the judge
		// runs on prompts that arrived while auto-approve was off.
		// ensureAutoApprove dedups against in-flight goroutines and
		// recorded verdicts, so this is safe to call unconditionally.
		// Errors here are non-fatal — the toggle itself succeeded; at
		// worst the user still has to answer the existing prompt
		// manually.
		if req.Enabled {
			s.resumeAutoApproveForPending(r.Context(), adapter, sessionID)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// resumeAutoApproveForPending lists every pending permission for the
// session and kicks off ensureAutoApprove on each. Called from the
// auto-approve enable path so prompts that arrived while auto-approve
// was off get judged as soon as the user opts in.
//
// Errors listing permissions are logged but otherwise swallowed: the
// caller is the toggle handler, which has already persisted the new
// state and must not fail on a best-effort resurrection.
func (s *Server) resumeAutoApproveForPending(ctx context.Context, adapter platforms.Platform, sessionID string) {
	entries, err := adapter.ListPermissions(ctx, sessionID)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"sessionID": sessionID,
			"platform":  adapter.ID(),
		}).Warn("auto-approve toggle: failed to list pending permissions for resume")
		return
	}
	for _, entry := range entries {
		permissionID, _ := entry["id"].(string)
		permission, _ := entry["permission"].(string)
		if permissionID == "" || permission == "" {
			continue
		}
		patterns := extractPermissionPatterns(entry)
		metadata := extractPermissionMetadata(entry)
		s.ensureAutoApprove(adapter.ID(), adapter, sessionID, permissionID, permission, patterns, metadata)
	}
}

// handleSessionApprovedPermissions handles GET /api/session/{id}/approved-permissions.
// Returns all permissions that were auto-approved by the LLM judge for this
// session, ordered by approval time. Used by the frontend to re-inject
// approval notices into the conversation thread after a page refresh.
func (s *Server) handleSessionApprovedPermissions(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		approved, err := s.stateDB.ListApprovedPermissions(string(adapter.ID()), sessionID)
		if err != nil {
			serverError(w, "listing approved permissions", err)
			return
		}
		type entry struct {
			PermissionID string   `json:"permissionId"`
			Permission   string   `json:"permission"`
			Patterns     []string `json:"patterns"`
			Reasoning    string   `json:"reasoning"`
			ApprovedAt   int64    `json:"approvedAt"`
		}
		out := make([]entry, 0, len(approved))
		for _, p := range approved {
			patterns := p.Patterns
			if patterns == nil {
				patterns = []string{}
			}
			out = append(out, entry{
				PermissionID: p.PermissionID,
				Permission:   p.PermissionText,
				Patterns:     patterns,
				Reasoning:    p.Reasoning,
				ApprovedAt:   p.ApprovedAt,
			})
		}
		writeJSON(w, out)
	})
}

// --- Session-scoped SSE event stream ---

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		s.serveSessionEvents(w, r, sessionID, adapter)
	})
}

func (s *Server) serveSessionEvents(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	ctx, span := telemetry.Tracer().Start(r.Context(), "GET /api/session/{id}/events",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("ocman.session_id", sessionID),
			attribute.String("ocman.platform", string(adapter.ID())),
			attribute.String("http.route", "/api/session/{id}/events"),
		),
	)
	defer span.End()

	if sseActiveConnections != nil {
		sseActiveConnections.Add(ctx, 1)
		defer sseActiveConnections.Add(ctx, -1)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, _ := w.(http.Flusher)
	var flush func()
	if flusher != nil {
		flush = flusher.Flush
	}

	// Wrap the writer so we can detect whether ProxyEvents ever produced
	// any output. When the platform is unreachable (no live OpenCode
	// instance for this session's directory), ProxyEvents returns
	// ErrPlatformUnreachable before writing a single byte. Without this
	// wrapper, the deferred status would be 200 + an empty body, which
	// the browser's EventSource treats as a successful stream that ended
	// cleanly — it then reconnects every ~500ms in a tight loop and
	// starves the HTTP/1.1 connection pool, blocking all other API
	// requests on the same origin.
	//
	// By keeping the status header unsent until the first real Write,
	// we can still emit HTTP 503 if ProxyEvents fails before producing
	// output. EventSource treats non-200 responses as a hard failure
	// and stops reconnecting — the connection slot is freed and the UI
	// recovers.
	lw := &lazyHeaderWriter{ResponseWriter: w}

	// Register this writer so non-SSE code paths (REST permission
	// listing, prompt resurrection on session re-open) can push
	// synthetic ocman.permission.* events into the same connection.
	// Both the tee's onPermission callback and handleSessionPermissions
	// flow through ensureAutoApprove → emitPermissionPending → this sink.
	// The deferred unregister both removes the registry entry and marks
	// the sink closed, so any in-flight backgroundAutoApprove emit
	// turns into a no-op rather than panicking on a recycled writer.
	sink := s.registerSseSink(sessionID, lw, flush)
	defer s.unregisterSseSink(sessionID, sink)

	// Tee the SSE stream so permission.asked events trigger server-side
	// auto-approve. This is one of two entry points into the
	// auto-approve pipeline; the other is runAutoApproveWatcher, which
	// keeps the pipeline running headlessly when no browser tab is
	// open. Both flow through ensureAutoApprove, which deduplicates
	// against in-flight goroutines so only one judge ever runs per
	// permission.
	//
	// OpenCode's /event stream is process-wide — every event for every
	// session in that OpenCode process flows through this connection.
	// The callback's `evtSessionID` argument carries the *event's*
	// session ID (extracted from the payload) so the auto-approve
	// pipeline routes the verdict, the persistence, and the
	// ocman.permission.* SSE event back to the correct session.
	// Using the connection's `sessionID` for routing was a bug — it
	// attributed every other session's auto-approved notice to
	// whichever session the user was currently viewing.
	tee := &ssePermissionTee{
		w:     lw,
		flush: flush,
		onPermission: func(evtSessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
			s.ensureAutoApprove(adapter.ID(), adapter, evtSessionID, permissionID, permission, patterns, metadata)
		},
		// permission.replied fires when the user (or any non-ocman
		// client, e.g. the OpenCode TUI) answers the prompt. Cancel
		// any in-flight judge so we stop polling immediately and the
		// verdict — if it arrives later — is discarded before it can
		// race the user's answer.
		onPermissionReplied: func(evtSessionID, permissionID string) {
			s.cancelAutoApprove(evtSessionID, permissionID)
		},
	}

	// For remote sessions, auto-approve is the owner's responsibility
	// (AD-14): the remote runs the judge with its own settings and emits
	// ocman.permission.* events into the stream, which the gRPC tunnel
	// forwards verbatim. The hub must NOT tee a remote stream into its
	// own judge, so we write events straight through.
	var dst io.Writer = tee
	if isRemotePlatformID(string(adapter.ID())) {
		dst = lw
	}

	err := adapter.ProxyEvents(ctx, sessionID, dst, flush)
	if err == nil {
		span.SetStatus(codes.Ok, "stream ended")
		return
	}
	if errors.Is(err, context.Canceled) {
		span.AddEvent("client disconnected")
		span.SetStatus(codes.Ok, "client disconnected")
		return
	}
	if errors.Is(err, platforms.ErrSSEIdleTimeout) {
		span.AddEvent("SSE idle timeout — client will reconnect")
		span.SetStatus(codes.Ok, "idle timeout")
		return
	}
	// Platform unreachable before any bytes flowed: send a real 503 so
	// EventSource gives up and frees the socket. Logged at Debug —
	// this is a normal steady state when OpenCode isn't running for
	// the session's directory (e.g. right after a machine reboot),
	// not a fault worth a WARN.
	if errors.Is(err, platforms.ErrPlatformUnreachable) && !lw.wrote {
		span.AddEvent("platform unreachable — returning 503")
		span.SetStatus(codes.Ok, "platform unreachable")
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		log.WithFields(log.Fields{"sessionID": sessionID}).
			Debug("SSE proxy: no running platform instance; returning 503")
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
		Warn("SSE proxy stream ended with error")
}

// lazyHeaderWriter delays the implicit 200 OK status write until the
// first real Write call. This lets serveSessionEvents emit a non-200
// status when ProxyEvents fails before producing any output (e.g. no
// live OpenCode instance for the session's directory).
//
// Once `wrote` flips to true the wrapper is a transparent pass-through;
// only the first Write needs the bookkeeping. WriteHeader is forwarded
// directly so anything that explicitly sets a status (the 503 fast-
// path below) bypasses the wrapper entirely.
type lazyHeaderWriter struct {
	http.ResponseWriter
	wrote bool
}

func (l *lazyHeaderWriter) Write(p []byte) (int, error) {
	if !l.wrote && len(p) > 0 {
		l.wrote = true
	}
	return l.ResponseWriter.Write(p)
}

// Flush forwards to the underlying ResponseWriter when it supports
// http.Flusher. SSE relies on Flush after every event, so this
// must work even when no bytes have been written yet.
func (l *lazyHeaderWriter) Flush() {
	if f, ok := l.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
