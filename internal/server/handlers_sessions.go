package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/convstream"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/srvtiming"
	"github.com/ocman/ocman/internal/state"
)

// --- Sessions aggregation ---

// handleSessions fans out to every registered Platform adapter for
// session data, then applies local state (archived / seen).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") == "sidebar" || r.URL.Query().Get("view") == "running-count" {
		s.handleSidebarSessions(w, r)
		return
	}
	dir := r.URL.Query().Get("dir")
	since := httpx.QueryInt[int64](r, "since", 0)
	limit := httpx.QueryInt(r, "limit", 500)
	if limit < 0 {
		limit = 500
	}

	ctx := r.Context()
	// Concurrent, non-blocking fan-out across local + remote adapters.
	// Remote adapters are bounded by a short timeout so a slow/offline
	// remote never delays the unified list (FR-15, NFR-1).
	fanPhase := srvtiming.Begin(ctx, "sessions_fanout")
	all := s.fanOutSessions(ctx, dir, since, s.registry.RememberSessions)
	fanPhase.End()
	// Force-include pinned and snoozed sessions outside the time window.
	// These sets are typically small; each miss is a single
	// adapter lookup. Silently skip sessions that are deleted or
	// inaccessible.
	preserved, _ := s.stateDB.PinnedSessions(ctx)
	if preserved == nil {
		preserved = make(map[state.Key]int64)
	}
	snoozed, _ := s.stateDB.SnoozedSessions(ctx)
	for key, until := range snoozed {
		preserved[key] = until
	}
	if len(preserved) > 0 {
		have := make(map[state.Key]bool, len(all))
		for _, sess := range all {
			have[state.Key{Platform: s.sessionStatePlatform(sess.Platform), SessionID: sess.ID}] = true
		}
		for key := range preserved {
			if have[key] {
				continue
			}
			route, proven := s.publicSessionPlatform(key.Platform, key.SessionID)
			adapter, ok := s.registry.Get(platforms.ID(route))
			if !proven || !ok || !adapter.Available(ctx) {
				continue
			}
			row, err := platforms.ReadSessionSummary(ctx, adapter, key.SessionID)
			if err != nil || row == nil {
				continue
			}
			if dir != "" && row.Directory != dir {
				continue
			}
			row.Platform = route
			all = append(all, *row)
		}
	}

	// Sort all platforms together by recency, then apply the limit.
	// Project sidebars request limit=0 to keep every session in the time window.
	if limit == 0 {
		limit = len(all)
	}
	all = sortAndLimitSessions(all, limit)

	statePhase := srvtiming.Begin(ctx, "state_overlay")
	err := s.applySessionState(ctx, all)
	statePhase.EndWithDesc("applySessionState")
	if err != nil {
		httpx.ServerError(w, "fetching session state", err)
		return
	}

	// Enrich errored sessions with normalized notices (e.g. rate-limit
	// backoff) so the frontend can surface the reason without
	// platform-specific parsing.
	applySessionNotice(all)
	for i := range all {
		s.applyFallNotice(all[i].Platform, &all[i])
	}

	// Note: git status info is no longer attached here. The
	// /api/sessions handler used to fan out up to 8 concurrent
	// `git status` subprocesses per request, which produced
	// fork-pressure pauses on macOS (multi-second hiccups across
	// unrelated handlers; see docs/other/profiling.md). Components that
	// need per-directory git state now request /api/git/info
	// explicitly while they're mounted, so subprocess work is
	// scoped to "the user is actually looking at this directory"
	// rather than "every dashboard poll, every 5 seconds".

	if all == nil {
		all = []db.Session{} // [] not null: the frontend maps over it
	}
	httpx.WriteJSON(w, all)
}

// --- Session detail ---

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	limit := httpx.QueryInt(r, "limit", 30)
	offset := httpx.QueryInt(r, "offset", 0)

	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}

	// History trails the live stream: compose what this hub streamed but
	// history has not stored yet. The read begins before history is queried,
	// so a read that started later cannot release content from this one.
	// Clients replay only events after the cursor and page older history
	// without counting the composed messages.
	read := s.convStreamModule().Hub().BeginRead(convstream.Key{Platform: string(adapter.ID()), SessionID: sessionID})
	defer read.End()
	detail, err := adapter.Session(r.Context(), sessionID, limit, offset)
	if err != nil {
		httpx.WritePlatformError(w, "fetching session", err)
		return
	}
	remote := isRemotePlatformID(string(adapter.ID()))
	composed := read.Compose(detail, limit, offset)
	s.conversationReadHeader(w, adapter, detail)
	s.enrichSessionDetail(r.Context(), string(adapter.ID()), sessionID, detail, !remote)
	s.enrichSessionCreator(r.Context(), string(adapter.ID()), sessionID, detail)
	routeKey := adapter.ID()
	if key, ok := s.registry.KeyFor(adapter); ok {
		routeKey = key
	}
	peek := r.URL.Query().Get("peek") == "1"
	if detail.Session != nil {
		detail.Session.ProjectDefaultModel = s.projectDefaultModel(r.Context(), detail.Session.Directory)
		if !peek || s.stateDB == nil {
			detail.Session.FactoryAttemptID, err = s.factoryAttemptID(r.Context(), string(routeKey), sessionID, detail.Session.ParentID)
			if err != nil {
				httpx.ServerError(w, "fetching Factory session tag", err)
				return
			}
		}
	}

	// Browsing preserves archive/snooze state. Use the same overlay and
	// activity-based resurface policy as the sidebar for local and remote rows.
	if s.stateDB != nil && detail.Session != nil {
		row := []db.Session{*detail.Session}
		row[0].Platform = string(routeKey)
		if err := s.applySessionState(r.Context(), row); err != nil {
			httpx.ServerError(w, "applying session state", err)
			return
		}
		detail.Session.Archived = row[0].Archived
		detail.Session.SnoozedUntil = row[0].SnoozedUntil
		detail.Session.Seen = row[0].Seen
		detail.Session.SeenTimeUpdated = row[0].SeenTimeUpdated
		detail.Session.RoutineID = row[0].RoutineID
		detail.Session.FactoryAttemptID = row[0].FactoryAttemptID
	}

	if key, ok := s.registry.KeyFor(adapter); ok {
		if detail.Session != nil {
			detail.Session.Platform = string(key)
		}
		for i := range detail.SessionTree {
			detail.SessionTree[i].Platform = string(key)
		}
	}
	body := map[string]interface{}{
		"session":           detail.Session,
		"sessionTree":       detail.SessionTree,
		"messages":          detail.Messages,
		"parts":             detail.Parts,
		"totalMessages":     detail.TotalMessages,
		"contextTokenCount": detail.ContextTokenCount,
		"defaultAgent":      detail.DefaultAgent,
		"defaultModel":      detail.DefaultModel,
	}
	if detail.Creator != nil {
		body["creator"] = detail.Creator
	}
	if composed.Cursor != "" || len(composed.MessageIDs) > 0 {
		body["stream"] = map[string]any{"cursor": composed.Cursor, "composedMessageIds": composed.MessageIDs}
	}
	httpx.WriteJSON(w, body)
}

func (s *Server) enrichSessionDetail(ctx context.Context, platform, sessionID string, detail *platforms.SessionDetail, approvals bool) {
	// `nil` slices would marshal as `null`; the frontend expects `[]`.
	if detail.Messages == nil {
		detail.Messages = []db.Message{}
	}
	if detail.Parts == nil {
		detail.Parts = []db.Part{}
	}
	if detail.Session != nil {
		detail.Session.Notice = deriveSessionNotice(*detail.Session)
		s.applyFallNotice(platform, detail.Session)
	}
	if approvals && s.stateDB != nil {
		// Remote details are enriched on their owner before crossing gRPC;
		// the hub must not read its own state DB for a remote session.
		injectApprovalNotices(ctx, platform, sessionID, s.stateDB, &detail.Messages, &detail.Parts)
		s.enrichInterruptionHistory(ctx, platform, sessionID, detail)
	}
}

// EnrichRemoteSessionDetail applies state owned by this instance before a
// Session RPC returns the detail to its hub.
func (s *Server) EnrichRemoteSessionDetail(ctx context.Context, platform, sessionID string, detail *platforms.SessionDetail) {
	s.enrichSessionDetail(ctx, platform, sessionID, detail, true)
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
// Response: { "tasks": {...} }
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

	limit := httpx.QueryInt(r, "limit", 10)
	if limit < 1 {
		limit = 1
	}
	if limit > 30 {
		limit = 30
	}

	type taskData struct {
		Platform string           `json:"platform"`
		Status   db.SessionStatus `json:"status,omitempty"`
		Messages json.RawMessage  `json:"messages"`
		Parts    json.RawMessage  `json:"parts"`
	}
	result := make(map[string]taskData, len(ids))
	for _, taskID := range ids {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}

		sessionID, platform := taskID, r.URL.Query().Get("platform")
		if strings.HasPrefix(taskID, "ocm_") || s.sessions.CanonicalSessionReferences(platform) {
			identity, err := s.sessions.LookupSessionReference(r.Context(), platform, taskID)
			if err != nil {
				writeIdentityError(w, err)
				return
			}
			sessionID, platform = s.sessions.CoreSessionReference(identity), identity.Platform
		}
		var adapter platforms.Platform
		var ok bool
		if platform != "" {
			adapter, ok = s.registry.Get(platforms.ID(platform))
			if !ok {
				writeIdentityError(w, platforms.ErrPlatformUnreachable)
				return
			}
		} else {
			adapter, ok = s.registry.PlatformForSession(r.Context(), sessionID)
		}
		if !ok {
			continue
		}

		detail, err := adapter.Session(r.Context(), sessionID, limit, 0)
		if err != nil {
			if errors.Is(err, platforms.ErrHistoryUnavailable) {
				httpx.WritePlatformError(w, "fetching stored task conversation", err)
				return
			}
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

		var status db.SessionStatus
		if detail.Session != nil {
			status = detail.Session.Status
		}
		result[taskID] = taskData{Platform: string(adapter.ID()), Status: status, Messages: msgsJSON, Parts: ptsJSON}
	}

	httpx.WriteJSON(w, map[string]interface{}{"tasks": result})
}
