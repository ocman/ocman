package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// --- Sessions aggregation ---

// handleSessions fans out to every registered Platform adapter for
// session data, then applies local state (archived / seen).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	since := parseInt64Param(r, "since", 0)
	limit := parseIntParam(r, "limit", 500)

	ctx := r.Context()
	var all []db.Session
	for _, adapter := range s.registry.Platforms() {
		if !adapter.Available(ctx) {
			continue
		}
		platPhase := srvtiming.Begin(ctx, "sessions_"+string(adapter.ID()))
		sessions, err := adapter.Sessions(ctx, dir, since)
		platPhase.End()
		if err != nil {
			log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
				Warn("listing sessions from platform")
			continue
		}
		s.registry.RememberSessions(adapter.ID(), sessions)
		all = append(all, sessions...)
	}

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
	var all []db.Session
	for _, adapter := range s.registry.Platforms() {
		if !adapter.Available(ctx) {
			continue
		}
		sessions, err := adapter.Sessions(ctx, "", since)
		if err != nil {
			log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
				Warn("listing sessions for notify")
			continue
		}
		all = append(all, sessions...)
	}

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

	// Inject persisted auto-approve notice messages/parts so they
	// arrive pre-sorted with the real messages. The frontend never
	// needs a separate fetch or client-side injection; the notices
	// land in chronological order alongside the real conversation.
	if s.stateDB != nil {
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
		stableKey := "ocman-notice-" + p.JudgeSessionID
		if existing[stableKey] {
			continue
		}
		existing[stableKey] = true

		patterns := p.Patterns
		if patterns == nil {
			patterns = []string{}
		}
		partData, _ := json.Marshal(map[string]interface{}{
			"type":           "auto-approved",
			"permission":     p.PermissionText,
			"patterns":       patterns,
			"judgeSessionId": p.JudgeSessionID,
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

func (s *Server) handleSessionAgents(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := adapter.AgentCatalog(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "fetching agent catalog", err)
			return
		}
		if entries == nil {
			entries = []platforms.AgentCatalogEntry{}
		}
		writeJSON(w, entries)
	})
}

func (s *Server) handleSessionCommands(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := adapter.SlashCommands(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "fetching slash commands", err)
			return
		}
		if entries == nil {
			entries = []platforms.SlashCommandEntry{}
		}
		writeJSON(w, entries)
	})
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
		writeJSON(w, entries)
	})
}

func (s *Server) handleSessionQuestions(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := adapter.ListQuestions(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "listing questions", err)
			return
		}
		if entries == nil {
			entries = []platforms.LivePrompt{}
		}
		writeJSON(w, entries)
	})
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
		w.WriteHeader(http.StatusNoContent)
	})
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
			PermissionID   string   `json:"permissionId"`
			Permission     string   `json:"permission"`
			Patterns       []string `json:"patterns"`
			JudgeSessionID string   `json:"judgeSessionId"`
			ApprovedAt     int64    `json:"approvedAt"`
		}
		out := make([]entry, 0, len(approved))
		for _, p := range approved {
			patterns := p.Patterns
			if patterns == nil {
				patterns = []string{}
			}
			out = append(out, entry{
				PermissionID:   p.PermissionID,
				Permission:     p.PermissionText,
				Patterns:       patterns,
				JudgeSessionID: p.JudgeSessionID,
				ApprovedAt:     p.ApprovedAt,
			})
		}
		writeJSON(w, out)
	})
}

// handleSessionPermissionJudge handles POST /api/session/{id}/permissions/{pid}/judge.
// Runs the LLM judge against the given permission and returns the verdict.
// The frontend calls this when auto-approve is enabled, shows a "checking"
// indicator while waiting, then either auto-submits (SAFE) or leaves the
// prompt for the human (UNSAFE).
func (s *Server) handleSessionPermissionJudge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Permission string   `json:"permission"`
		Patterns   []string `json:"patterns"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string, adapter platforms.Platform) {
		// Resolve the session directory so the judge can find the
		// running OpenCode instance's port.
		if s.db == nil {
			http.Error(w, "OpenCode platform not available", http.StatusServiceUnavailable)
			return
		}
		dbSession, err := s.db.GetSession(sessionID)
		if err != nil || dbSession == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		// Extract the permission ID from the URL path: "permissions/{pid}/judge"
		permissionID := strings.TrimPrefix(rest, "permissions/")
		permissionID = strings.TrimSuffix(permissionID, "/judge")

		result := s.judge.Judge(r.Context(), dbSession.Directory, req.Permission, req.Patterns)

		// Persist the approval so the notice survives a page refresh.
		if result.Verdict == verdictSafe && s.stateDB != nil {
			if err := s.stateDB.RecordApprovedPermission(
				string(adapter.ID()),
				sessionID,
				state.ApprovedPermission{
					PermissionID:   permissionID,
					PermissionText: req.Permission,
					Patterns:       req.Patterns,
					JudgeSessionID: result.SessionID,
					ApprovedAt:     time.Now().UnixMilli(),
				},
			); err != nil {
				log.WithError(err).Warn("failed to persist auto-approved permission")
				// Non-fatal — the verdict is still returned to the frontend.
			}
		}

		writeJSON(w, map[string]interface{}{
			"verdict":        string(result.Verdict),
			"judgeSessionId": result.SessionID,
		})
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

	if err := adapter.ProxyEvents(ctx, sessionID, w, flush); err != nil {
		if errors.Is(err, context.Canceled) {
			span.AddEvent("client disconnected")
			span.SetStatus(codes.Ok, "client disconnected")
			return
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
			Warn("SSE proxy stream ended with error")
		return
	}
	span.SetStatus(codes.Ok, "stream ended")
}
