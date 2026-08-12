package server

import (
	"encoding/json"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
	"github.com/NoUseFreak/ocman/internal/state"
)

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
	// unrelated handlers; see docs/other/profiling.md). Components that
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
	ID                string           `json:"id"`
	Status            db.SessionStatus `json:"status"`
	Seen              bool             `json:"seen"`
	PendingPermission bool             `json:"pendingPermission,omitempty"`
	PendingQuestion   bool             `json:"pendingQuestion,omitempty"`
	Title             string           `json:"title,omitempty"`
	Directory         string           `json:"directory,omitempty"`
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
		isUnseenTerminal := (se.Status == db.StatusWaiting || se.Status == db.StatusError) && !se.Seen
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
		// Local-only path (remote sessions are skipped above), so the
		// project being unarchived is the hub's own copy.
		if err := s.stateDB.UnarchiveProject(state.LocalRemoteID, projectRootForDirectory(detail.Session.Directory)); err != nil {
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
		// "Allow always" records are retained for child-session inheritance,
		// not conversation notices; a person, not the AI, made that approval.
		if p.Reasoning == "user clicked Allow always" {
			continue
		}
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
// Response: { "tasks": {...}, "children": [{ "id", "intent", "status", "createdAt" }] }
func (s *Server) handleSessionTasks(w http.ResponseWriter, r *http.Request) {
	idsParam := r.URL.Query().Get("ids")
	var ids []string
	if idsParam != "" {
		ids = strings.Split(idsParam, ",")
	}
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
	type childData struct {
		ID        string `json:"id"`
		Intent    string `json:"intent"`
		Status    string `json:"status"`
		CreatedAt int64  `json:"createdAt"`
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

	children := []childData{}
	if s.stateDB != nil {
		if rows, err := s.stateDB.ListChildSessionsByParent(r.PathValue("id")); err == nil {
			children = make([]childData, 0, len(rows))
			for _, child := range rows {
				children = append(children, childData{
					ID: child.ID, Intent: child.Intent, Status: child.Status, CreatedAt: child.CreatedAt,
				})
			}
		}
	}
	writeJSON(w, map[string]interface{}{"tasks": result, "children": children})
}
