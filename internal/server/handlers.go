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
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/pricing"
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
// response. The three well-known sentinels (ErrUnsupported, ErrNotFound)
// map to 501/404; every other error becomes a 502 Bad Gateway (the
// most likely cause is an unreachable upstream platform).
func writePlatformError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, platforms.ErrUnsupported) {
		http.Error(w, "operation not supported by this platform", http.StatusNotImplemented)
		return
	}
	if errors.Is(err, platforms.ErrNotFound) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	log.WithError(err).Error(msg)
	http.Error(w, "failed to reach platform instance", http.StatusBadGateway)
}

// --- Global read-only endpoints ---

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetStats()
	if err != nil {
		serverError(w, "fetching stats", err)
		return
	}
	writeJSON(w, stats)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
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

	metrics, err := s.db.GetMetricsDashboard(agent, model, since, dayCount, limit, offset, sessionLimit, sessionOffset, pricing.Load())
	if err != nil {
		serverError(w, "fetching metrics", err)
		return
	}
	writeJSON(w, metrics)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.GetProjects()
	if err != nil {
		serverError(w, "fetching projects", err)
		return
	}
	writeJSON(w, projects)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	since := parseSinceParam(r)
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	activity, err := s.db.GetDailyActivity(since, model)
	if err != nil {
		serverError(w, "fetching activity", err)
		return
	}
	writeJSON(w, activity)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	since := parseSinceParam(r)
	models, err := s.db.GetModelUsage(since)
	if err != nil {
		serverError(w, "fetching model usage", err)
		return
	}
	writeJSON(w, models)
}

func (s *Server) handleHourlyTokens(w http.ResponseWriter, r *http.Request) {
	since := parseSinceParam(r)
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	var dayCount int
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		fmt.Sscanf(v, "%d", &dayCount)
	}
	data, err := s.db.GetHourlyTokensByModel(dayCount, since, model)
	if err != nil {
		serverError(w, "fetching hourly tokens by model", err)
		return
	}
	writeJSON(w, data)
}

func (s *Server) handleHourly(w http.ResponseWriter, r *http.Request) {
	since := parseSinceParam(r)
	hourly, err := s.db.GetHourlyActivity(since)
	if err != nil {
		serverError(w, "fetching hourly activity", err)
		return
	}
	writeJSON(w, hourly)
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

	ctx := r.Context()
	var all []db.Session
	for _, adapter := range s.registry.Platforms() {
		if !adapter.Available(ctx) {
			continue
		}
		sessions, err := adapter.Sessions(ctx, dir, since)
		if err != nil {
			log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
				Warn("listing sessions from platform")
			continue
		}
		s.registry.RememberSessions(adapter.ID(), sessions)
		all = append(all, sessions...)
	}

	if err := s.applySessionState(all); err != nil {
		serverError(w, "fetching session state", err)
		return
	}

	writeJSON(w, all)
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

	for i := range sessions {
		seenAtUpdate, ok := seen[sessions[i].ID]
		if ok && seenAtUpdate >= sessions[i].TimeUpdated {
			sessions[i].Seen = true
		}

		archivedAtUpdate, ok := archived[sessions[i].ID]
		if !ok {
			continue
		}
		if sessions[i].TimeUpdated > archivedAtUpdate {
			if err := s.stateDB.UnarchiveSession(sessions[i].ID); err != nil {
				return err
			}
			continue
		}
		sessions[i].Archived = true
	}

	return nil
}

// --- Archive / seen state ---

func (s *Server) handleSeenSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
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

	if err := s.stateDB.MarkSessionSeen(req.SessionID, req.SessionTimeUpdated); err != nil {
		serverError(w, "updating seen session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleArchiveSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
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

	var err error
	if req.Archived {
		err = s.stateDB.ArchiveSession(req.SessionID, req.SessionTimeUpdated)
	} else {
		err = s.stateDB.UnarchiveSession(req.SessionID)
	}
	if err != nil {
		serverError(w, "updating archived session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

// --- Session detail (GET /api/session/{id}) ---

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session/")

	// Parse pagination params
	limit := 50
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
		Model string `json:"model"`
		Agent string `json:"agent"`
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
	})
	if err != nil {
		writePlatformError(w, "executing command", err)
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, _ := w.(http.Flusher)
	var flush func()
	if flusher != nil {
		flush = flusher.Flush
	}

	if err := adapter.ProxyEvents(r.Context(), sessionID, w, flush); err != nil {
		// At this point headers are already sent, so the best we can
		// do is log and close.
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
			Warn("SSE proxy stream ended with error")
	}
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
//	/api/session/{id}                  GET      -> handleSession
//	/api/session/{id}/agents           GET      -> handleSessionAgents
//	/api/session/{id}/commands         GET      -> handleSessionCommands
//	/api/session/{id}/models           GET      -> handleSessionModels
//	/api/session/{id}/permissions      GET      -> handleSessionPermissions
//	/api/session/{id}/permissions/{pid} POST    -> handleSessionPermission
//	/api/session/{id}/questions        GET      -> handleSessionQuestions
//	/api/session/{id}/questions/{qid}  POST     -> handleSessionQuestion
//	/api/session/{id}/questions/{qid}/reject POST -> handleSessionQuestion
//	/api/session/{id}/message          POST     -> handleSessionMessage
//	/api/session/{id}/command          POST     -> handleSessionCommand
//	/api/session/{id}/abort            POST     -> handleSessionAbort
//	/api/session/{id}/compact          POST     -> handleSessionCompact
//	/api/session/{id}/events           GET      -> handleSessionEvents
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
	}

	// Split {id}/{rest}.
	slash := strings.IndexByte(trimmed, '/')
	if slash < 0 {
		// /api/session/{id}
		if r.Method == http.MethodGet {
			s.handleSession(w, r)
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
		}
	case http.MethodPost:
		switch head {
		case "message":
			s.handleSessionMessage(w, r)
			return
		case "command":
			s.handleSessionCommand(w, r)
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

	resp, err := adapter.CreateSession(r.Context(), platforms.CreateSessionRequest{Directory: req.Directory})
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
		"platforms": out,
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

// enforce that the "context" import is referenced at least once from
// this file; most uses are via r.Context() but we call out one explicit
// use for readers scanning the top of the file.
var _ = context.Background
