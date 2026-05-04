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
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// maxRequestBody is the maximum allowed request body size (1 MB).
const maxRequestBody = 1 << 20

// maxSendMessageBody is the maximum allowed body for send-message
// (20 MB to support inline images).
const maxSendMessageBody = 20 << 20

// maxAudioUpload is the maximum allowed audio upload size (25 MB).
const maxAudioUpload = 25 << 20

// validIDPattern matches safe session/resource IDs (alphanumeric, hyphens, underscores).
var validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateID checks that an ID is safe for use in URLs and log messages.
func validateID(id string) bool {
	return id != "" && len(id) <= 256 && validIDPattern.MatchString(id)
}

// readAndUnmarshal reads the request body (up to maxBytes) and unmarshals
// it into dst. Returns false and writes an HTTP error if reading or
// parsing fails.
func readAndUnmarshal(w http.ResponseWriter, r *http.Request, maxBytes int64, dst interface{}) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

// --- Platform dispatch helpers ---

// resolvePlatformForSession returns the Platform adapter owning a
// given session ID. Writes 404 and returns nil if no adapter claims it.
func (s *Server) resolvePlatformForSession(w http.ResponseWriter, r *http.Request, sessionID string) platforms.Platform {
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return nil
	}
	p, ok := s.registry.PlatformForSession(r.Context(), sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return nil
	}
	return p
}

// writePlatformError maps a Platform error to an appropriate HTTP
// response. Well-known sentinels map to specific status codes:
//
//   - ErrUnsupported         -> 501 Not Implemented
//   - ErrNotFound            -> 404 Not Found
//   - ErrBusy                -> 409 Conflict (AD-13, Claude Code composer
//     refusing while session is mid-turn)
//   - ErrPlatformUnreachable -> 503 Service Unavailable (no live instance
//     discovered for the session's directory; the frontend uses this
//     to offer a one-click tmux launch of opencode)
//   - ErrUpstreamRejected    -> 422 Unprocessable Entity (the platform
//     was reached but rejected the request, e.g. an unknown model;
//     the upstream-supplied human message is forwarded as the body
//     so the UI can show it to the user)
//
// Every other error becomes a 502 Bad Gateway (the most likely
// cause is an unreachable upstream platform).
func writePlatformError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, platforms.ErrUnsupported) {
		http.Error(w, "operation not supported by this platform", http.StatusNotImplemented)
		return
	}
	if errors.Is(err, platforms.ErrNotFound) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, platforms.ErrBusy) {
		// 409 is the standard "your request is valid but conflicts
		// with the current state of the resource" code. The client
		// should retry once the session reports idle.
		http.Error(w, "session is currently processing a prompt; try again in a moment", http.StatusConflict)
		return
	}
	if errors.Is(err, platforms.ErrPlatformUnreachable) {
		// 503 signals "the upstream isn't running right now, try
		// again later." The frontend recognises this status and
		// offers to launch opencode via tmux.
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, platforms.ErrUpstreamRejected) {
		// 422 = "we reached the platform and it told us no." Pass
		// the upstream-supplied message straight through so the UI
		// can render the real reason (e.g. ProviderModelNotFoundError
		// when the user picks a model the OpenCode instance hasn't
		// authenticated for). Still log it so server-side debugging
		// has the wrapped form.
		log.WithError(err).Warn(msg)
		var ue *platforms.UpstreamError
		body := "the platform rejected the request"
		if errors.As(err, &ue) && ue.Message != "" {
			body = ue.Message
		}
		http.Error(w, body, http.StatusUnprocessableEntity)
		return
	}
	log.WithError(err).Error(msg)
	http.Error(w, "failed to reach platform instance", http.StatusBadGateway)
}

// --- Global read-only endpoints ---

// requireDB returns true if s.db is available, or writes a 501 error
// and returns false. Callers that depend on the OpenCode database
// should gate on this.
func (s *Server) requireDB(w http.ResponseWriter) bool {
	if s.db == nil {
		http.Error(w, "OpenCode platform is not enabled", http.StatusNotImplemented)
		return false
	}
	return true
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	stats, err := s.db.GetStats()
	if err != nil {
		serverError(w, "fetching stats", err)
		return
	}
	writeJSON(w, stats)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	var since int64
	var dayCount int
	if daysStr := strings.TrimSpace(r.URL.Query().Get("days")); daysStr != "" && daysStr != "0" {
		fmt.Sscanf(daysStr, "%d", &dayCount)
		if dayCount > 0 {
			since = time.Now().Add(-time.Duration(dayCount) * 24 * time.Hour).UnixMilli()
		}
	}
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	var offset int
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}
	sessionLimit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("sessionLimit")); v != "" {
		fmt.Sscanf(v, "%d", &sessionLimit)
	}
	var sessionOffset int
	if v := strings.TrimSpace(r.URL.Query().Get("sessionOffset")); v != "" {
		fmt.Sscanf(v, "%d", &sessionOffset)
	}
	projectLimit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("projectLimit")); v != "" {
		fmt.Sscanf(v, "%d", &projectLimit)
	}
	var projectOffset int
	if v := strings.TrimSpace(r.URL.Query().Get("projectOffset")); v != "" {
		fmt.Sscanf(v, "%d", &projectOffset)
	}
	dir := normaliseDirParam(r.URL.Query().Get("dir"))

	metrics, err := s.db.GetMetricsDashboard(agent, model, since, dayCount, limit, offset, sessionLimit, sessionOffset, projectLimit, projectOffset, pricing.Load(), dir)
	if err != nil {
		serverError(w, "fetching metrics", err)
		return
	}
	writeJSON(w, metrics)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	projects, loaded := s.projectsSnapshot()
	if !loaded {
		if err := s.refreshProjectsIndex(); err != nil {
			serverError(w, "fetching projects", err)
			return
		}
		projects, _ = s.projectsSnapshot()
	}
	writeJSON(w, projects)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	activity, err := s.db.GetDailyActivity(since, model, dir)
	if err != nil {
		serverError(w, "fetching activity", err)
		return
	}
	writeJSON(w, activity)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	models, err := s.db.GetModelUsage(since, dir)
	if err != nil {
		serverError(w, "fetching model usage", err)
		return
	}
	writeJSON(w, models)
}

func (s *Server) handleHourlyTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	var dayCount int
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		fmt.Sscanf(v, "%d", &dayCount)
	}
	data, err := s.db.GetHourlyTokensByModel(dayCount, since, model, dir)
	if err != nil {
		serverError(w, "fetching hourly tokens by model", err)
		return
	}
	writeJSON(w, data)
}

func (s *Server) handleHourly(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	hourly, err := s.db.GetHourlyActivity(since, dir)
	if err != nil {
		serverError(w, "fetching hourly activity", err)
		return
	}
	writeJSON(w, hourly)
}

// normaliseDirParam trims surrounding whitespace and a single trailing slash
// from a directory-prefix filter so that "/repo/foo", "/repo/foo/", and
// "  /repo/foo  " are all treated the same. Returns "" when the input is
// blank, which the DB layer interprets as "no filter".
func normaliseDirParam(raw string) string {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return ""
	}
	// Strip exactly one trailing slash. We don't strip more than one because
	// "//" is a meaningful absolute path on some platforms (and never a
	// session.directory value we'd see in practice — defensive only).
	if strings.HasSuffix(dir, "/") && dir != "/" {
		dir = dir[:len(dir)-1]
	}
	return dir
}

// parseSinceParam reads the ?days= query param and returns a Unix
// millisecond cutoff. Returns 0 (no filter) when the param is absent
// or zero.
func parseSinceParam(r *http.Request) int64 {
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" && v != "0" {
		var dayCount int64
		fmt.Sscanf(v, "%d", &dayCount)
		if dayCount > 0 {
			return time.Now().Add(-time.Duration(dayCount) * 24 * time.Hour).UnixMilli()
		}
	}
	return 0
}

// --- Sessions aggregation ---

// handleSessions fans out to every registered Platform adapter for
// session data, then applies local state (archived / seen).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	var since int64
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		fmt.Sscanf(sinceStr, "%d", &since)
	}
	limit := 500
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

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

	// Adapters each return their own list pre-sorted, but the combined
	// slice must also be sorted so sessions from different platforms
	// interleave by recency rather than clumping by source.
	// Sessions are bucketed into 5-minute windows (floor(timeUpdated/300s))
	// so that small timestamp differences within the same window don't
	// cause constant re-ordering. Within a bucket, newer sessions sort first.
	const bucketMs = 5 * 60 * 1000
	sort.SliceStable(all, func(i, j int) bool {
		bi, bj := all[i].TimeUpdated/bucketMs, all[j].TimeUpdated/bucketMs
		if bi != bj {
			return bi > bj
		}
		if all[i].ProjectID != all[j].ProjectID {
			return all[i].ProjectID < all[j].ProjectID
		}
		return all[i].Title < all[j].Title
	})

	// Apply limit
	if len(all) > limit {
		all = all[:limit]
	}

	statePhase := srvtiming.Begin(ctx, "state_overlay")
	err := s.applySessionState(all)
	statePhase.EndWithDesc("applySessionState")
	if err != nil {
		serverError(w, "fetching session state", err)
		return
	}

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
	var since int64
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		fmt.Sscanf(sinceStr, "%d", &since)
	}
	limit := 500
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

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

	sort.SliceStable(all, func(i, j int) bool {
		const bucketMs = 5 * 60 * 1000
		bi, bj := all[i].TimeUpdated/bucketMs, all[j].TimeUpdated/bucketMs
		if bi != bj {
			return bi > bj
		}
		if all[i].ProjectID != all[j].ProjectID {
			return all[i].ProjectID < all[j].ProjectID
		}
		return all[i].Title < all[j].Title
	})
	if len(all) > limit {
		all = all[:limit]
	}

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

func (s *Server) applySessionState(sessions []db.Session) error {
	archived, err := s.stateDB.ArchivedSessions()
	if err != nil {
		return err
	}
	seen, err := s.stateDB.SeenSessions()
	if err != nil {
		return err
	}
	pinned, err := s.stateDB.PinnedSessions()
	if err != nil {
		return err
	}

	for i := range sessions {
		key := state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID}

		seenAtUpdate, ok := seen[key]
		if ok && seenAtUpdate >= sessions[i].TimeUpdated {
			sessions[i].Seen = true
		}

		if pinnedAt, ok := pinned[key]; ok {
			sessions[i].Pinned = true
			sessions[i].PinnedAt = pinnedAt
		}

		archivedAtUpdate, ok := archived[key]
		if !ok {
			continue
		}
		if sessions[i].TimeUpdated > archivedAtUpdate {
			if err := s.stateDB.UnarchiveSession(key.Platform, key.SessionID); err != nil {
				return err
			}
			continue
		}
		sessions[i].Archived = true
	}

	return nil
}

// --- Archive / seen state ---

// resolvePlatformIDForState returns the platform ID owning a session
// for state.db operations. The caller may also pass an explicit
// platform string in the request body to short-circuit the lookup
// (used when archiving / marking-seen a session whose platform the
// client already knows from its local cache). Empty platform triggers
// a registry reverse lookup.
func (s *Server) resolvePlatformIDForState(w http.ResponseWriter, r *http.Request, sessionID, platform string) (string, bool) {
	if platform != "" {
		if _, ok := s.registry.Get(platforms.ID(platform)); !ok {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return "", false
		}
		return platform, true
	}
	p, ok := s.registry.PlatformForSession(r.Context(), sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return "", false
	}
	return string(p.ID()), true
}

func (s *Server) handleSeenSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform           string `json:"platform"`
		SessionID          string `json:"sessionId"`
		SessionTimeUpdated int64  `json:"timeUpdated"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.SessionTimeUpdated <= 0 {
		http.Error(w, "timeUpdated is required", http.StatusBadRequest)
		return
	}
	platform, ok := s.resolvePlatformIDForState(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	if err := s.stateDB.MarkSessionSeen(platform, req.SessionID, req.SessionTimeUpdated); err != nil {
		serverError(w, "updating seen session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleArchiveSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform           string `json:"platform"`
		SessionID          string `json:"sessionId"`
		SessionTimeUpdated int64  `json:"timeUpdated"`
		Archived           bool   `json:"archived"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.Archived && req.SessionTimeUpdated <= 0 {
		http.Error(w, "timeUpdated is required", http.StatusBadRequest)
		return
	}
	platform, ok := s.resolvePlatformIDForState(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	var err error
	if req.Archived {
		err = s.stateDB.ArchiveSession(platform, req.SessionID, req.SessionTimeUpdated)
	} else {
		err = s.stateDB.UnarchiveSession(platform, req.SessionID)
	}
	if err != nil {
		serverError(w, "updating archived session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePinSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform  string `json:"platform"`
		SessionID string `json:"sessionId"`
		Pinned    bool   `json:"pinned"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	platform, ok := s.resolvePlatformIDForState(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	var err error
	if req.Pinned {
		err = s.stateDB.PinSession(platform, req.SessionID)
	} else {
		err = s.stateDB.UnpinSession(platform, req.SessionID)
	}
	if err != nil {
		serverError(w, "updating pinned session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

// --- Session detail (GET /api/session/{id}) ---

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session/")

	// Parse pagination params
	limit := 30
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}

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

// handleSessionTasks returns the latest tool output for a batch of
// running sub-task sessions. The frontend uses this to show live
// previews of Task tool calls without making N separate
// /api/session/{taskId}?limit=1 requests (P7 fix).
//
// Query params:
//   - ids: comma-separated list of task session IDs.
//
// Response: { "tasks": { "<taskId>": "<latest output text>", ... } }
func (s *Server) handleSessionTasks(w http.ResponseWriter, r *http.Request) {
	idsParam := r.URL.Query().Get("ids")
	if idsParam == "" {
		writeJSON(w, map[string]interface{}{"tasks": map[string]string{}})
		return
	}

	ids := strings.Split(idsParam, ",")
	const maxBatch = 20
	if len(ids) > maxBatch {
		ids = ids[:maxBatch]
	}

	result := make(map[string]string, len(ids))
	for _, taskID := range ids {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}

		adapter, ok := s.registry.PlatformForSession(r.Context(), taskID)
		if !ok {
			continue
		}

		detail, err := adapter.Session(r.Context(), taskID, 1, 0)
		if err != nil {
			continue
		}

		// Walk the latest assistant message's parts to find tool output.
		var stdout string
		for i := len(detail.Messages) - 1; i >= 0; i-- {
			m := detail.Messages[i]
			// Parse the message data to check the role.
			var md struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal(m.Data, &md); err != nil || md.Role != "assistant" {
				continue
			}
			for _, p := range detail.Parts {
				if p.MessageID != m.ID {
					continue
				}
				// Parse the part data to extract state.output.
				var pd struct {
					State struct {
						Output interface{} `json:"output"`
					} `json:"state"`
				}
				if err := json.Unmarshal(p.Data, &pd); err != nil {
					continue
				}
				if out, ok := pd.State.Output.(string); ok && out != "" {
					stdout = out
					break
				}
			}
			if stdout != "" {
				break
			}
		}
		if stdout != "" {
			result[taskID] = stdout
		}
	}

	writeJSON(w, map[string]interface{}{"tasks": result})
}

// --- Session-scoped read endpoints ---

// sessionSubPath splits "/api/session/{id}/{rest}" into id and rest.
// Returns ok=false if the path shape is wrong. The ID is validated
// against validateID; rest is returned as-is for the caller to further
// dispatch on.
func sessionSubPath(path, basePrefix string) (id, rest string, ok bool) {
	trimmed := strings.TrimPrefix(path, basePrefix)
	if trimmed == path {
		return "", "", false
	}
	slash := strings.IndexByte(trimmed, '/')
	if slash < 0 {
		return trimmed, "", true
	}
	return trimmed[:slash], trimmed[slash+1:], true
}

// handleSessionAgents returns the platform's composer-agent catalog
// for a session (OpenCode's "build"/"plan"/subagent roles). Platforms
// that don't have a catalog return an empty array.
func (s *Server) handleSessionAgents(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	entries, err := adapter.AgentCatalog(r.Context(), sessionID)
	if err != nil {
		writePlatformError(w, "fetching agent catalog", err)
		return
	}
	if entries == nil {
		entries = []platforms.AgentCatalogEntry{}
	}
	writeJSON(w, entries)
}

func (s *Server) handleSessionCommands(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	entries, err := adapter.SlashCommands(r.Context(), sessionID)
	if err != nil {
		writePlatformError(w, "fetching slash commands", err)
		return
	}
	if entries == nil {
		entries = []platforms.SlashCommandEntry{}
	}
	writeJSON(w, entries)
}

func (s *Server) handleSessionModels(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	resp, err := adapter.SessionModels(r.Context(), sessionID)
	if err != nil {
		writePlatformError(w, "fetching session models", err)
		return
	}
	if resp == nil {
		resp = &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}
	}
	writeJSON(w, resp)
}

func (s *Server) handleSessionPermissions(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	entries, err := adapter.ListPermissions(r.Context(), sessionID)
	if err != nil {
		writePlatformError(w, "listing permissions", err)
		return
	}
	if entries == nil {
		entries = []platforms.LivePrompt{}
	}
	writeJSON(w, entries)
}

func (s *Server) handleSessionQuestions(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	entries, err := adapter.ListQuestions(r.Context(), sessionID)
	if err != nil {
		writePlatformError(w, "listing questions", err)
		return
	}
	if entries == nil {
		entries = []platforms.LivePrompt{}
	}
	writeJSON(w, entries)
}

// handleSessionChanges aggregates every file-touching tool call in a
// session into a per-file changes summary. Adapters that don't support
// the operation (Claude Code) are surfaced as a Supported=false payload
// rather than an HTTP error so the frontend has a single shape to
// render. See spec/session-changes-sidebar/architecture.md AD-2.
func (s *Server) handleSessionChanges(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	changes, err := adapter.SessionChanges(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, platforms.ErrUnsupported) {
			writeJSON(w, &platforms.SessionChanges{
				SessionID: sessionID,
				Supported: false,
				Files:     []platforms.FileChange{},
			})
			return
		}
		writePlatformError(w, "fetching session changes", err)
		return
	}
	if changes == nil {
		changes = &platforms.SessionChanges{
			SessionID: sessionID,
			Supported: false,
			Files:     []platforms.FileChange{},
		}
	}
	if changes.Files == nil {
		changes.Files = []platforms.FileChange{}
	}
	writeJSON(w, changes)
}

// handleSessionInfo returns the per-session info snapshot consumed by
// the right-hand "Session info" panel: lifetime token totals, the
// latest todowrite list, and (when the platform has a live channel)
// context-window size, configured MCP servers, and configured LSP
// servers. Mirrors handleSessionChanges in shape: adapters that don't
// support the operation at all (Claude Code today) surface as
// Supported=false rather than an HTTP error so the frontend has a
// single shape to render. OpenCode without a live port returns
// Supported=false from the adapter directly (with the always-on
// fields populated from the DB) — the ErrUnsupported branch below
// covers the all-or-nothing platforms only.
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	info, err := adapter.SessionInfo(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, platforms.ErrUnsupported) {
			writeJSON(w, &platforms.SessionInfo{
				SessionID:  sessionID,
				Supported:  false,
				MCPServers: []platforms.MCPServer{},
				LSPServers: []platforms.LSPServer{},
			})
			return
		}
		writePlatformError(w, "fetching session info", err)
		return
	}
	if info == nil {
		info = &platforms.SessionInfo{
			SessionID:  sessionID,
			Supported:  false,
			MCPServers: []platforms.MCPServer{},
			LSPServers: []platforms.LSPServer{},
		}
	}
	if info.MCPServers == nil {
		info.MCPServers = []platforms.MCPServer{}
	}
	if info.LSPServers == nil {
		info.LSPServers = []platforms.LSPServer{}
	}
	writeJSON(w, info)
}

// --- Session-scoped mutating endpoints ---

func (s *Server) handleSessionMessage(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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

	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}

	images := make([]platforms.ImageAttachment, 0, len(req.Images))
	for _, img := range req.Images {
		images = append(images, platforms.ImageAttachment{URL: img.URL, Mime: img.Mime})
	}
	err := adapter.SendMessage(r.Context(), platforms.SendMessageRequest{
		SessionID: sessionID,
		Message:   req.Message,
		Images:    images,
		Model:     req.Model,
		Agent:     req.Agent,
		Reasoning: req.Reasoning,
	})
	if err != nil {
		writePlatformError(w, "sending message", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionCommand(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	err := adapter.ExecuteCommand(r.Context(), platforms.ExecuteCommandRequest{
		SessionID: sessionID,
		Command:   req.Command,
		Arguments: req.Arguments,
		Model:     req.Model,
		Agent:     req.Agent,
		Reasoning: req.Reasoning,
	})
	if err != nil {
		writePlatformError(w, "executing command", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionShell handles POST /api/session/{id}/shell — runs a
// raw shell command in the session's working directory, bypassing the
// LLM. Used by the composer's `!`-prefix routing on platforms that
// declare caps.shellExec; adapters without the capability return
// ErrUnsupported (mapped to 501).
func (s *Server) handleSessionShell(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	if err := adapter.RunShell(r.Context(), platforms.RunShellRequest{
		SessionID: sessionID,
		Command:   req.Command,
		Agent:     req.Agent,
	}); err != nil {
		writePlatformError(w, "running shell command", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	if err := adapter.RenameSession(r.Context(), platforms.RenameSessionRequest{
		SessionID: sessionID,
		Title:     req.Title,
	}); err != nil {
		writePlatformError(w, "renaming session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionAbort(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	if err := adapter.Abort(r.Context(), platforms.AbortRequest{SessionID: sessionID}); err != nil {
		writePlatformError(w, "aborting session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	err := adapter.Compact(r.Context(), platforms.CompactRequest{
		SessionID:  sessionID,
		ProviderID: req.ProviderID,
		ModelID:    req.ModelID,
	})
	if err != nil {
		writePlatformError(w, "compacting session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionPermission handles POST /api/session/{id}/permissions/{pid}
func (s *Server) handleSessionPermission(w http.ResponseWriter, r *http.Request) {
	sessionID, rest, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	permissionID := strings.TrimPrefix(rest, "permissions/")
	if !validateID(permissionID) {
		http.Error(w, "invalid permission ID", http.StatusBadRequest)
		return
	}
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
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	err := adapter.RespondPermission(r.Context(), platforms.RespondPermissionRequest{
		SessionID:    sessionID,
		PermissionID: permissionID,
		Reply:        req.Reply,
	})
	if err != nil {
		writePlatformError(w, "responding to permission", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionQuestion dispatches POST /api/session/{id}/questions/{qid}
// and POST /api/session/{id}/questions/{qid}/reject.
func (s *Server) handleSessionQuestion(w http.ResponseWriter, r *http.Request) {
	sessionID, rest, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
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
	err := adapter.RespondQuestion(r.Context(), platforms.RespondQuestionRequest{
		SessionID: sessionID,
		RequestID: questionID,
		Answers:   req.Answers,
	})
	if err != nil {
		writePlatformError(w, "responding to question", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Session-scoped SSE event stream ---

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}

	// SSE is filtered out of otelhttp's auto-spanning (see otel.go) so
	// we manage the connection-lifetime span manually here. Naming it
	// after the route template keeps cardinality low; the session id
	// goes on as an attribute so traces can still be filtered by it.
	ctx, span := telemetry.Tracer().Start(r.Context(), "GET /api/session/{id}/events",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("ocman.session_id", sessionID),
			attribute.String("ocman.platform", string(adapter.ID())),
			attribute.String("http.route", "/api/session/{id}/events"),
		),
	)
	defer span.End()

	// Track concurrent SSE connections so dashboards can spot stuck
	// streams. The decrement is deferred to cover the early-exit
	// paths below (header errors etc.).
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

// --- /api/sessions (GET = list, POST = create) ---

func (s *Server) handleSessionsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleSessions(w, r)
	case http.MethodPost:
		s.handleCreateSession(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// dispatchSessionSubpath routes every /api/session/... request to the
// right session-scoped handler. Structure:
//
//	/api/session/archive               POST     -> handleArchiveSession
//	/api/session/seen                  POST     -> handleSeenSession
//	/api/session/pin                   POST     -> handlePinSession
//	/api/session/{id}                  GET      -> handleSession
//	/api/session/{id}                  PATCH    -> handleSessionRename
//	/api/session/{id}/agents           GET      -> handleSessionAgents
//	/api/session/{id}/commands         GET      -> handleSessionCommands
//	/api/session/{id}/models           GET      -> handleSessionModels
//	/api/session/{id}/changes          GET      -> handleSessionChanges
//	/api/session/{id}/info             GET      -> handleSessionInfo
//	/api/session/{id}/permissions      GET      -> handleSessionPermissions
//	/api/session/{id}/permissions/{pid} POST    -> handleSessionPermission
//	/api/session/{id}/questions        GET      -> handleSessionQuestions
//	/api/session/{id}/questions/{qid}  POST     -> handleSessionQuestion
//	/api/session/{id}/questions/{qid}/reject POST -> handleSessionQuestion
//	/api/session/{id}/message          POST     -> handleSessionMessage
//	/api/session/{id}/command          POST     -> handleSessionCommand
//	/api/session/{id}/shell            POST     -> handleSessionShell
//	/api/session/{id}/abort            POST     -> handleSessionAbort
//	/api/session/{id}/compact          POST     -> handleSessionCompact
//	/api/session/{id}/events           GET      -> handleSessionEvents
//	/api/session/{id}/tasks            GET      -> handleSessionTasks
func (s *Server) dispatchSessionSubpath(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/session/")

	// Non-session reserved subpaths.
	switch trimmed {
	case "archive":
		if r.Method == http.MethodPost {
			s.handleArchiveSession(w, r)
			return
		}
	case "seen":
		if r.Method == http.MethodPost {
			s.handleSeenSession(w, r)
			return
		}
	case "pin":
		if r.Method == http.MethodPost {
			s.handlePinSession(w, r)
			return
		}
	}

	// Split {id}/{rest}.
	slash := strings.IndexByte(trimmed, '/')
	if slash < 0 {
		// /api/session/{id}
		if r.Method == http.MethodGet {
			s.handleSession(w, r)
			return
		}
		if r.Method == http.MethodPatch {
			s.handleSessionRename(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := trimmed[slash+1:]
	head := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		head = rest[:i]
	}

	// Route by (method, head).
	switch r.Method {
	case http.MethodGet:
		switch head {
		case "agents":
			s.handleSessionAgents(w, r)
			return
		case "commands":
			s.handleSessionCommands(w, r)
			return
		case "models":
			s.handleSessionModels(w, r)
			return
		case "changes":
			s.handleSessionChanges(w, r)
			return
		case "info":
			s.handleSessionInfo(w, r)
			return
		case "permissions":
			// GET /api/session/{id}/permissions (no pid)
			if rest == "permissions" {
				s.handleSessionPermissions(w, r)
				return
			}
		case "questions":
			if rest == "questions" {
				s.handleSessionQuestions(w, r)
				return
			}
		case "events":
			s.handleSessionEvents(w, r)
			return
		case "tasks":
			s.handleSessionTasks(w, r)
			return
		}
	case http.MethodPost:
		switch head {
		case "message":
			s.handleSessionMessage(w, r)
			return
		case "command":
			s.handleSessionCommand(w, r)
			return
		case "shell":
			s.handleSessionShell(w, r)
			return
		case "abort":
			s.handleSessionAbort(w, r)
			return
		case "compact":
			s.handleSessionCompact(w, r)
			return
		case "permissions":
			// POST /api/session/{id}/permissions/{pid}
			s.handleSessionPermission(w, r)
			return
		case "questions":
			// POST /api/session/{id}/questions/{qid}
			// POST /api/session/{id}/questions/{qid}/reject
			s.handleSessionQuestion(w, r)
			return
		}
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// --- /api/sessions POST (create session) ---

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform  string `json:"platform"`
		Directory string `json:"directory"`
		Title     string `json:"title"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}

	// Resolve the target platform. If the caller didn't specify one,
	// default to the single registered platform (common case for ocman
	// today). Platforms that aren't Available are skipped so ocman
	// doesn't try to create a Claude Code session when Claude Code
	// isn't installed.
	var adapter platforms.Platform
	if req.Platform != "" {
		p, ok := s.registry.Get(platforms.ID(req.Platform))
		if !ok {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return
		}
		adapter = p
	} else {
		for _, p := range s.registry.Platforms() {
			if !p.Available(r.Context()) {
				continue
			}
			if adapter != nil {
				http.Error(w, "multiple platforms available — specify ?platform=<id>", http.StatusBadRequest)
				return
			}
			adapter = p
		}
	}
	if adapter == nil {
		http.Error(w, "no platform available to create a session", http.StatusServiceUnavailable)
		return
	}

	resp, err := adapter.CreateSession(r.Context(), platforms.CreateSessionRequest{
		Directory: req.Directory,
		Title:     req.Title,
	})
	if err != nil {
		writePlatformError(w, "creating session", err)
		return
	}
	writeJSON(w, resp)
}

// --- Capabilities endpoint ---

// capabilityEntry mirrors Platform identity + capability flags on the
// wire. Kept explicit so the frontend's TS type stays small and stable.
type capabilityEntry struct {
	ID           string                 `json:"id"`
	DisplayName  string                 `json:"displayName"`
	Available    bool                   `json:"available"`
	Capabilities platforms.Capabilities `json:"capabilities"`
}

// handleCapabilities returns the current capability set of every
// registered platform so the frontend can gate UI without branching
// on platform identity.
//
// In addition to per-platform flags, the response carries a small set
// of *server-wide* capability booleans for features whose availability
// depends on the host environment rather than any one platform —
// today that's just `worktreeSessions` (AD-7).
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := make([]capabilityEntry, 0)
	for _, p := range s.registry.Platforms() {
		out = append(out, capabilityEntry{
			ID:           string(p.ID()),
			DisplayName:  p.DisplayName(),
			Available:    p.Available(ctx),
			Capabilities: p.Capabilities(),
		})
	}
	writeJSON(w, map[string]interface{}{
		"platforms":        out,
		"worktreeSessions": worktreeSessionsAvailable(s.registry),
	})
}

// --- Whisper transcription (platform-independent) ---

func (s *Server) handleWhisperStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"available": whisperAvailable(),
	})
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if !whisperAvailable() {
		http.Error(w, "whisper is not available", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUpload)

	file, header, err := r.FormFile("audio")
	if err != nil {
		log.WithError(err).Warn("failed to read audio upload")
		http.Error(w, "failed to read audio file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".wav"
	}
	tmp, err := os.CreateTemp("", "ocman-audio-*"+ext)
	if err != nil {
		serverError(w, "creating temp file", err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, file); err != nil {
		serverError(w, "writing audio to temp file", err)
		return
	}
	tmp.Close()

	text, err := transcribeAudio(tmp.Name())
	if err != nil {
		log.WithError(err).Error("transcription failed")
		http.Error(w, "transcription failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"text": text})
}

// --- Cost calculator (platform-independent) ---

func (s *Server) handleCalcCost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelID    string `json:"modelID"`
		Input      int64  `json:"input"`
		Output     int64  `json:"output"`
		CacheRead  int64  `json:"cacheRead"`
		CacheWrite int64  `json:"cacheWrite"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}

	table := pricing.Load()
	cost := table.CalcCost(req.ModelID, req.Input, req.Output, req.CacheRead, req.CacheWrite)
	price := table.Lookup(req.ModelID)
	known := price.InputPerToken != 0 || price.OutputPerToken != 0

	writeJSON(w, map[string]interface{}{
		"cost":  cost,
		"known": known,
	})
}

// --- Debug log sink ---

func (s *Server) handleDebugLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level   string          `json:"level"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}

	fields := log.Fields{"source": "fe"}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		fields["ua"] = ua
	}
	if len(req.Data) > 0 {
		fields["data"] = string(req.Data)
	}

	entry := log.WithFields(fields)
	switch strings.ToLower(strings.TrimSpace(req.Level)) {
	case "error":
		entry.Error(req.Message)
	case "warn", "warning":
		entry.Warn(req.Message)
	case "debug":
		entry.Debug(req.Message)
	default:
		entry.Info(req.Message)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleSystemStats returns backend runtime statistics (memory usage, uptime, etc).
//
// The `db` block is included only when ocman has an OpenCode read-only
// handle (i.e. when the opencode platform adapter is registered). It
// surfaces database/sql's connection-pool stats so we can watch for
// the failure modes documented in docs/profiling.md:
//
//   - wait_count climbing → request concurrency is exceeding the
//     pool cap; queries queue. Either bump maxOpenReadConns or look
//     for a hot path running too many parallel queries.
//   - in_use staying high while idle is 0 → long-running transactions
//     are pinning connections, which prevents OpenCode from
//     checkpointing the WAL.
//   - open_conns growing toward max_open_conns over time even when
//     idle → handle leak somewhere.
func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := map[string]interface{}{
		"memory": map[string]interface{}{
			"alloc":        m.Alloc,        // bytes currently allocated
			"totalAlloc":   m.TotalAlloc,   // cumulative bytes allocated
			"sys":          m.Sys,          // bytes obtained from OS
			"heapAlloc":    m.HeapAlloc,    // bytes allocated on heap
			"heapSys":      m.HeapSys,      // bytes obtained from OS for heap
			"heapInuse":    m.HeapInuse,    // bytes in in-use spans
			"heapIdle":     m.HeapIdle,     // bytes in idle spans
			"heapReleased": m.HeapReleased, // bytes released to OS
		},
		"gc": map[string]interface{}{
			"numGC":   m.NumGC,
			"lastGC":  m.LastGC,
			"pauseNs": m.PauseNs[(m.NumGC+255)%256], // most recent GC pause
		},
		"goroutines": runtime.NumGoroutine(),
		"uptime":     time.Since(s.startTime).Seconds(),
	}

	if s.db != nil {
		ds := s.db.Stats()
		stats["db"] = map[string]interface{}{
			"max_open_conns":   ds.MaxOpenConnections,
			"open_conns":       ds.OpenConnections,
			"in_use":           ds.InUse,
			"idle":             ds.Idle,
			"wait_count":       ds.WaitCount,
			"wait_duration_ms": ds.WaitDuration.Milliseconds(),
		}
	}

	writeJSON(w, stats)
}

// enforce that the "context" import is referenced at least once from
// this file; most uses are via r.Context() but we call out one explicit
// use for readers scanning the top of the file.
var _ = context.Background
