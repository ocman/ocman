package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/pricing"
)

// maxRequestBody is the maximum allowed request body size (1 MB).
const maxRequestBody = 1 << 20

// validIDPattern matches safe session/resource IDs (alphanumeric, hyphens, underscores).
var validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateID checks that an ID is safe for use in URLs and log messages.
func validateID(id string) bool {
	return id != "" && len(id) <= 256 && validIDPattern.MatchString(id)
}

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

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	var since int64
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		fmt.Sscanf(sinceStr, "%d", &since)
	}
	sessions, err := s.db.GetSessions(dir, since)
	if err != nil {
		serverError(w, "fetching sessions", err)
		return
	}
	if err := s.applySessionState(sessions); err != nil {
		serverError(w, "fetching session state", err)
		return
	}

	// Discover all running OpenCode ports once and mark sessions.
	ports := discoverOpenCodePorts()
	// Best-effort: fetch pending permission/question prompts from each
	// running OpenCode instance so the UI can pull the user's attention
	// to sessions that are waiting for a response. If any instance fails
	// or is missing the endpoint, the corresponding flags simply stay
	// false — they are hints, not critical state.
	pendingPerms, pendingQuestions := collectPendingPromptsByDir(ports)
	for i := range sessions {
		sessions[i].Platform = "opencode"
		if _, ok := ports[sessions[i].Directory]; ok {
			sessions[i].LiveConnection = true
		}
		if pendingPerms[sessions[i].ID] {
			sessions[i].PendingPermission = true
		}
		if pendingQuestions[sessions[i].ID] {
			sessions[i].PendingQuestion = true
		}
	}

	writeJSON(w, sessions)
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

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session/")
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	// Parse pagination params
	limit := 50 // default: last 50 messages
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}

	// Try live data from OpenCode API first
	if data, ok := s.fetchSessionFromOpenCode(sessionID, limit, offset); ok {
		writeJSON(w, data)
		return
	}

	// Fall back to DB
	session, err := s.db.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
		} else {
			serverError(w, "fetching session", err)
		}
		return
	}
	session.Platform = "opencode"

	messages, err := s.db.GetSessionMessages(sessionID)
	if err != nil {
		serverError(w, "fetching session messages", err)
		return
	}

	parts, err := s.db.GetSessionParts(sessionID)
	if err != nil {
		serverError(w, "fetching session parts", err)
		return
	}

	// Apply pagination to DB results
	totalMessages := len(messages)
	pagedMessages, _ := db.PaginateMessages(messages, limit, offset)
	filteredParts := db.FilterPartsForMessages(parts, pagedMessages)

	// Compute composer context usage from assistant message total token counts.
	contextTokenCount, err := s.db.GetContextTokenCount(sessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Warn("fetching context token count")
	}
	defaults, err := s.db.GetSessionDefaults(sessionID, session.Directory)
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Warn("fetching session defaults")
	}

	writeJSON(w, map[string]interface{}{
		"session":           session,
		"messages":          pagedMessages,
		"parts":             filteredParts,
		"totalMessages":     totalMessages,
		"contextTokenCount": contextTokenCount,
		"defaultAgent":      defaults.Agent,
		"defaultModel":      defaults.Model,
	})
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

// sessionModelEntry is one row in the /api/session-models response. Fields
// are chosen for the composer picker — no costs/limits/capabilities. Ordering
// and badging signals (IsSessionDefault, RecentRank, IsProviderDefault) are
// computed server-side so the client just renders.
type sessionModelEntry struct {
	Provider     string `json:"provider"`
	ProviderName string `json:"providerName,omitempty"`
	Model        string `json:"model"`
	ModelName    string `json:"modelName,omitempty"`
	// RecentRank is the 1-based position in the "recently used" list, or 0
	// if this model wasn't among the recents. Lower = more recent.
	RecentRank        int  `json:"recentRank,omitempty"`
	IsSessionDefault  bool `json:"isSessionDefault,omitempty"`
	IsProviderDefault bool `json:"isProviderDefault,omitempty"`
	IsAvailable       bool `json:"isAvailable,omitempty"` // provider is in `connected`
}

// sessionModelsResponse is the shape returned by GET /api/session-models/{id}.
type sessionModelsResponse struct {
	// SessionDefault is "providerID/modelID" or "" — the session's preferred
	// model (last agent message's model, else most recent session default).
	SessionDefault string `json:"sessionDefault,omitempty"`
	// ProviderDefaults maps providerID -> modelID (from OpenCode's /provider).
	ProviderDefaults map[string]string `json:"providerDefaults,omitempty"`
	// HasProviders indicates the list includes live /provider data. When
	// false, the client only sees recents (from the DB).
	HasProviders bool                `json:"hasProviders"`
	Models       []sessionModelEntry `json:"models"`
}

// handleSessionModels returns the merged model list for a session's composer:
// recents from the DB (cheap query) + live-available models from OpenCode's
// `/provider` endpoint (filtered to connected providers) when reachable.
// Session and provider defaults are marked. Deprecated (status != "active")
// models are filtered out, unless they're the session default or among the
// recents — hiding something the user just used would be confusing.
func (s *Server) handleSessionModels(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session-models/")
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	session, err := s.db.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "session not found", http.StatusNotFound)
		} else {
			serverError(w, "fetching session for models", err)
		}
		return
	}

	// Cheap index-backed query: distinct models across the ~50 most recently
	// updated sessions, newest first.
	recents, err := s.db.GetRecentModels(50, 10)
	if err != nil {
		log.WithError(err).Warn("fetching recent models for /session-models")
	}

	// Session default — same precedence as handleSession: last assistant
	// message model, falling back to most recent session default across dirs.
	defaults, err := s.db.GetSessionDefaults(sessionID, session.Directory)
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Warn("fetching session defaults for /session-models")
	}
	sessionDefault := defaults.Model

	// Live-available models (best-effort). When no OpenCode instance is
	// running for this session's directory, we still return recents.
	var providers OpenCodeProvidersResponse
	hasProviders := false
	if port := discoverOpenCodePort(session.Directory); port != "" {
		providers, hasProviders = fetchOpenCodeProviders(port)
	}

	entries := buildSessionModelEntries(recents, providers, hasProviders, sessionDefault)

	// Only surface defaults for connected providers. Returning all 115
	// catalog defaults would bloat the response for no UI benefit.
	var connectedDefaults map[string]string
	if hasProviders && len(providers.Default) > 0 {
		connectedDefaults = make(map[string]string, len(providers.Connected))
		for _, id := range providers.Connected {
			if m, ok := providers.Default[id]; ok {
				connectedDefaults[id] = m
			}
		}
	}

	writeJSON(w, sessionModelsResponse{
		SessionDefault:   sessionDefault,
		ProviderDefaults: connectedDefaults,
		HasProviders:     hasProviders,
		Models:           entries,
	})
}

// buildSessionModelEntries merges recents, live /provider data, and the
// session default into a single sorted list. Split out from the handler so
// it's unit-testable.
//
// Sort order:
//  1. Session default (⭐)
//  2. Recents, preserving recency order
//  3. Provider defaults (that aren't already above)
//  4. All remaining available models, alphabetical by provider then model
//  5. Any remaining DB-only recents (only reachable when providers weren't)
func buildSessionModelEntries(
	recents []db.RecentModel,
	providers OpenCodeProvidersResponse,
	hasProviders bool,
	sessionDefault string,
) []sessionModelEntry {
	key := func(provider, model string) string { return provider + "/" + model }

	entryMap := make(map[string]*sessionModelEntry)
	get := func(provider, model string) *sessionModelEntry {
		k := key(provider, model)
		if e, ok := entryMap[k]; ok {
			return e
		}
		e := &sessionModelEntry{Provider: provider, Model: model}
		entryMap[k] = e
		return e
	}

	// Seed from recents so they're always known to the merger, even when
	// the provider no longer appears in /provider.
	for i, rm := range recents {
		e := get(rm.Provider, rm.Model)
		e.RecentRank = i + 1
	}

	// Index the connected provider set for cheap lookup.
	connected := make(map[string]struct{}, len(providers.Connected))
	for _, id := range providers.Connected {
		connected[id] = struct{}{}
	}

	// Live-available models override names and mark availability. Filter to
	// connected providers only — showing 115 unconfigured providers would be
	// useless noise.
	providerName := make(map[string]string, len(providers.All))
	for _, p := range providers.All {
		providerName[p.ID] = p.Name
		if _, ok := connected[p.ID]; !ok {
			continue
		}
		for modelID, m := range p.Models {
			// Hide deprecated models unless we have a reason to show them.
			if m.Status != "" && m.Status != "active" {
				if _, seen := entryMap[key(p.ID, modelID)]; !seen && sessionDefault != key(p.ID, modelID) {
					continue
				}
			}
			e := get(p.ID, modelID)
			e.ProviderName = p.Name
			e.ModelName = m.Name
			e.IsAvailable = true
		}
	}
	// Back-fill provider names on entries that aren't in the live set (e.g.
	// recents from a provider the user removed).
	for _, e := range entryMap {
		if e.ProviderName == "" {
			if name := providerName[e.Provider]; name != "" {
				e.ProviderName = name
			}
		}
	}

	// Mark session default + provider defaults.
	if sessionDefault != "" {
		if e, ok := entryMap[sessionDefault]; ok {
			e.IsSessionDefault = true
		} else if slash := strings.IndexByte(sessionDefault, '/'); slash > 0 {
			// Session default refers to a model we haven't seen elsewhere —
			// still surface it so it's selectable.
			e := get(sessionDefault[:slash], sessionDefault[slash+1:])
			e.IsSessionDefault = true
		}
	}
	for providerID, modelID := range providers.Default {
		if e, ok := entryMap[key(providerID, modelID)]; ok {
			e.IsProviderDefault = true
		}
	}

	// Collect and sort.
	out := make([]sessionModelEntry, 0, len(entryMap))
	for _, e := range entryMap {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		// 1. session default first
		if a.IsSessionDefault != b.IsSessionDefault {
			return a.IsSessionDefault
		}
		// 2. recents before non-recents; within recents, preserve rank
		aRecent, bRecent := a.RecentRank > 0, b.RecentRank > 0
		if aRecent != bRecent {
			return aRecent
		}
		if aRecent && bRecent {
			return a.RecentRank < b.RecentRank
		}
		// 3. provider defaults before non-defaults
		if a.IsProviderDefault != b.IsProviderDefault {
			return a.IsProviderDefault
		}
		// 4. available before unavailable
		if a.IsAvailable != b.IsAvailable {
			return a.IsAvailable
		}
		// 5. alphabetical by provider then model
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Model < b.Model
	})
	// When we got no live provider data, flip IsAvailable off so the client
	// doesn't try to distinguish "archived" vs "available" in the UI.
	if !hasProviders {
		for i := range out {
			out[i].IsAvailable = false
		}
	}
	return out
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

// parseSinceParam reads the ?days= query param and returns a Unix millisecond cutoff.
// Returns 0 (no filter) when the param is absent or zero.
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

func (s *Server) handleSessionPort(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session-port/")
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	session, err := s.db.GetSession(sessionID)
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Error("fetching session for port lookup")
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	port := discoverOpenCodePort(session.Directory)
	writeJSON(w, map[string]interface{}{
		"port":      port,
		"available": port != "",
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		log.WithField("directory", req.Directory).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this directory", http.StatusServiceUnavailable)
		return
	}

	resp, err := openCodeClient.Post(
		fmt.Sprintf("http://127.0.0.1:%s/session", port),
		"application/json",
		limitedReader([]byte("{}")),
	)
	if err != nil {
		log.WithFields(log.Fields{"port": port, "error": err}).Error("failed to reach OpenCode instance")
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.WithFields(log.Fields{"statusCode": resp.StatusCode, "body": string(respBody)}).Error("OpenCode API error")
		http.Error(w, "OpenCode API error", resp.StatusCode)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)
}

// maxSendMessageBody is the maximum allowed body for send-message (20 MB to support inline images).
const maxSendMessageBody = 20 << 20

type openCodeModelRef struct {
	ProviderID string `json:"providerID,omitempty"`
	ModelID    string `json:"modelID"`
}

func parseOpenCodeModelRef(model string) *openCodeModelRef {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	providerID, modelID, ok := strings.Cut(model, "/")
	if ok {
		providerID = strings.TrimSpace(providerID)
		modelID = strings.TrimSpace(modelID)
		if providerID != "" && modelID != "" {
			return &openCodeModelRef{ProviderID: providerID, ModelID: modelID}
		}
	}
	return &openCodeModelRef{ModelID: model}
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
		Message   string `json:"message"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Images    []struct {
			URL  string `json:"url"`
			Mime string `json:"mime"`
		} `json:"images"`
	}
	if !readAndUnmarshal(w, r, maxSendMessageBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.Message == "" && len(req.Images) == 0 {
		http.Error(w, "message or images required", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID, "images": len(req.Images)}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	// Build parts array: text + optional images
	parts := []map[string]string{}
	if req.Message != "" {
		parts = append(parts, map[string]string{"type": "text", "text": req.Message})
	}
	for _, img := range req.Images {
		parts = append(parts, map[string]string{
			"type": "file",
			"url":  img.URL,
			"mime": img.Mime,
		})
	}

	// Use prompt_async so the request returns immediately (204) and the
	// assistant response streams back via SSE instead of blocking until
	// the full response is generated.
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/prompt_async", port, req.SessionID)
	bodyMap := map[string]interface{}{
		"parts": parts,
	}
	if modelRef := parseOpenCodeModelRef(req.Model); modelRef != nil {
		bodyMap["model"] = modelRef
	}
	if req.Agent != "" {
		bodyMap["agent"] = req.Agent
	}
	payload, _ := json.Marshal(bodyMap)

	log.WithFields(logCtx).WithField("port", port).Info("sending message via OpenCode API")
	proxyToOpenCode(w, req.Directory, apiURL, payload, logCtx)
}

// proxyToOpenCode handles the common pattern of discovering an OpenCode port,
// POSTing a payload, and forwarding the response. It returns 204 on success.
func proxyToOpenCode(w http.ResponseWriter, directory, apiURL string, payload []byte, logContext log.Fields) {
	resp, err := openCodeClient.Post(apiURL, "application/json", limitedReader(payload))
	if err != nil {
		log.WithFields(logContext).WithError(err).Error("OpenCode API error")
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.WithFields(logContext).WithFields(log.Fields{"statusCode": resp.StatusCode, "body": string(respBody)}).Error("OpenCode API error")
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("OpenCode API error (HTTP %d)", resp.StatusCode)
		}
		http.Error(w, errMsg, resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// requireOpenCodePort discovers the OpenCode port for a directory, returning it
// or writing a 503 error to the response. Returns "" if unavailable.
func requireOpenCodePort(w http.ResponseWriter, directory string, logContext log.Fields) string {
	port := discoverOpenCodePort(directory)
	if port == "" {
		log.WithFields(logContext).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
	}
	return port
}

// readAndUnmarshal reads the request body (up to maxBytes) and unmarshals it into dst.
// Returns false and writes an HTTP error if reading or parsing fails.
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

func (s *Server) handleRespondPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID    string `json:"sessionId"`
		Directory    string `json:"directory"`
		PermissionID string `json:"permissionId"`
		Reply        string `json:"reply"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) || !validateID(req.PermissionID) {
		http.Error(w, "invalid session or permission ID", http.StatusBadRequest)
		return
	}
	if req.Reply == "" {
		http.Error(w, "reply is required", http.StatusBadRequest)
		return
	}
	switch req.Reply {
	case "once", "always", "reject":
		// valid
	default:
		http.Error(w, "invalid reply value: expected once, always, or reject", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID, "permissionID": req.PermissionID}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/permissions/%s", port, req.SessionID, req.PermissionID)
	payload, _ := json.Marshal(map[string]interface{}{"response": req.Reply})
	proxyToOpenCode(w, req.Directory, apiURL, payload, logCtx)
}

// handleListPermissions proxies GET /permission/ from the running OpenCode instance
// to retrieve any currently pending permission requests. This allows the frontend
// to recover pending permissions after connecting to the SSE stream.
func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir parameter required", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"directory": dir}
	port := discoverOpenCodePort(dir)
	if port == "" {
		log.WithFields(logCtx).Warn("no running OpenCode instance found")
		// Return empty list when no instance is running — not an error.
		writeJSON(w, []interface{}{})
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/permission", port)
	resp, err := openCodeClient.Get(apiURL)
	if err != nil {
		log.WithFields(logCtx).WithError(err).Error("failed to list permissions from OpenCode")
		writeJSON(w, []interface{}{})
		return
	}
	defer resp.Body.Close()

	// If the OpenCode instance doesn't support the /permission/ endpoint it may
	// return a non-JSON response (e.g. its SPA HTML fallback). Treat anything
	// that isn't application/json as an empty list.
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "application/json") {
		log.WithFields(logCtx).WithFields(log.Fields{
			"status":      resp.StatusCode,
			"contentType": ct,
		}).Debug("OpenCode /permission/ endpoint returned non-JSON response, treating as empty")
		writeJSON(w, []interface{}{})
		return
	}

	var permissions []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&permissions); err != nil {
		log.WithFields(logCtx).WithError(err).Error("failed to decode permissions response")
		writeJSON(w, []interface{}{})
		return
	}

	writeJSON(w, permissions)
}

// handleListQuestions proxies GET /question from the running OpenCode instance
// to retrieve any currently pending question prompts. This allows the frontend
// to recover pending questions that were asked before the user opened the
// session (the SSE question.asked event fires only once, so a user who wasn't
// viewing the session when it fired would otherwise never see the prompt).
func (s *Server) handleListQuestions(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir parameter required", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"directory": dir}
	port := discoverOpenCodePort(dir)
	if port == "" {
		log.WithFields(logCtx).Warn("no running OpenCode instance found")
		// Return empty list when no instance is running — not an error.
		writeJSON(w, []interface{}{})
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/question", port)
	resp, err := openCodeClient.Get(apiURL)
	if err != nil {
		log.WithFields(logCtx).WithError(err).Error("failed to list questions from OpenCode")
		writeJSON(w, []interface{}{})
		return
	}
	defer resp.Body.Close()

	// If the OpenCode instance doesn't support the /question endpoint it may
	// return a non-JSON response (e.g. its SPA HTML fallback). Treat anything
	// that isn't application/json as an empty list.
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "application/json") {
		log.WithFields(logCtx).WithFields(log.Fields{
			"status":      resp.StatusCode,
			"contentType": ct,
		}).Debug("OpenCode /question endpoint returned non-JSON response, treating as empty")
		writeJSON(w, []interface{}{})
		return
	}

	var questions []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&questions); err != nil {
		log.WithFields(logCtx).WithError(err).Error("failed to decode questions response")
		writeJSON(w, []interface{}{})
		return
	}

	writeJSON(w, questions)
}

func (s *Server) handleRespondQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string     `json:"sessionId"`
		Directory string     `json:"directory"`
		RequestID string     `json:"requestId"`
		Answers   [][]string `json:"answers"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) || !validateID(req.RequestID) {
		http.Error(w, "invalid session or request ID", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID, "requestID": req.RequestID}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/question/%s/reply", port, req.RequestID)
	payload, _ := json.Marshal(map[string]interface{}{"answers": req.Answers})
	proxyToOpenCode(w, req.Directory, apiURL, payload, logCtx)
}

func (s *Server) handleRejectQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
		RequestID string `json:"requestId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) || !validateID(req.RequestID) {
		http.Error(w, "invalid session or request ID", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID, "requestID": req.RequestID}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/question/%s/reject", port, req.RequestID)
	proxyToOpenCode(w, req.Directory, apiURL, []byte("{}"), logCtx)
}

func (s *Server) handleAbortSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/abort", port, req.SessionID)
	proxyToOpenCode(w, req.Directory, apiURL, []byte("{}"), logCtx)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir parameter required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(dir)
	if port == "" {
		log.WithField("directory", dir).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found", http.StatusServiceUnavailable)
		return
	}

	// Connect to the OpenCode SSE endpoint
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/event", port)
	req, err := http.NewRequestWithContext(r.Context(), "GET", apiURL, nil)
	if err != nil {
		serverError(w, "creating SSE request", err)
		return
	}

	// Use a client without the normal timeout for long-lived SSE connections
	sseClient := &http.Client{}
	resp, err := sseClient.Do(req)
	if err != nil {
		log.WithError(err).Error("failed to connect to OpenCode SSE")
		http.Error(w, "failed to connect to OpenCode", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Proxy the stream
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			if err != io.EOF {
				log.WithFields(log.Fields{"directory": dir, "error": err}).Warn("SSE proxy stream ended with error")
			}
			return
		}
	}
}

// maxAudioUpload is the maximum allowed audio upload size (25 MB).
const maxAudioUpload = 25 << 20

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

	// Limit upload size
	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUpload)

	file, header, err := r.FormFile("audio")
	if err != nil {
		log.WithError(err).Warn("failed to read audio upload")
		http.Error(w, "failed to read audio file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Write to a temp file so whisper can read it
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

	writeJSON(w, map[string]interface{}{
		"text": text,
	})
}

// handleCommands proxies GET /command from the OpenCode instance to list available slash commands.
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	s.proxyOpenCodeGET(w, r, "/command", "commands")
}

// handleAgents proxies GET /agent from the OpenCode instance to list available agents
// (primary and subagent) along with their configured model, description and color.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	s.proxyOpenCodeGET(w, r, "/agent", "agents")
}

// proxyOpenCodeGET forwards a GET request to the OpenCode instance for the directory
// supplied via the "dir" query parameter. label is used for log messages/errors.
func (s *Server) proxyOpenCodeGET(w http.ResponseWriter, r *http.Request, path, label string) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir parameter required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(dir)
	if port == "" {
		log.WithField("directory", dir).Warnf("no running OpenCode instance found (for %s)", label)
		http.Error(w, "no running OpenCode instance found", http.StatusServiceUnavailable)
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	resp, err := openCodeClient.Get(apiURL)
	if err != nil {
		log.WithFields(log.Fields{"directory": dir, "error": err}).Errorf("OpenCode %s API error", label)
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("OpenCode %s API error (HTTP %d)", label, resp.StatusCode)
		}
		http.Error(w, errMsg, resp.StatusCode)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// handleExecuteCommand proxies POST /session/:id/command to execute a slash command.
func (s *Server) handleExecuteCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
		Command   string `json:"command"`
		Arguments string `json:"arguments"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID, "command": req.Command}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/command", port, req.SessionID)
	bodyMap := map[string]interface{}{
		"command":   req.Command,
		"arguments": req.Arguments,
	}
	if req.Model != "" {
		bodyMap["model"] = req.Model
	}
	if req.Agent != "" {
		bodyMap["agent"] = req.Agent
	}
	payload, _ := json.Marshal(bodyMap)

	log.WithFields(logCtx).WithField("port", port).Info("executing command via OpenCode API")
	proxyToOpenCode(w, req.Directory, apiURL, payload, logCtx)
}

// handleCompactSession proxies POST /session/:id/summarize to compact a session's history.
func (s *Server) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID  string `json:"sessionId"`
		Directory  string `json:"directory"`
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.ProviderID == "" || req.ModelID == "" {
		http.Error(w, "providerID and modelID are required", http.StatusBadRequest)
		return
	}

	logCtx := log.Fields{"sessionID": req.SessionID, "providerID": req.ProviderID, "modelID": req.ModelID}
	port := requireOpenCodePort(w, req.Directory, logCtx)
	if port == "" {
		return
	}

	// Prefer OpenCode's configured `small_model` for compaction. Summarizing
	// conversation history is a lightweight task and running it on the main
	// (potentially expensive reasoning) model is wasteful. OpenCode exposes the
	// merged config at /config; when `small_model` is set we use it, otherwise
	// we fall back to whatever the frontend passed in (typically the currently
	// active model).
	providerID, modelID := req.ProviderID, req.ModelID
	if p, m, ok := fetchOpenCodeSmallModel(port); ok {
		providerID, modelID = p, m
		logCtx["smallModelProviderID"] = p
		logCtx["smallModelID"] = m
	}

	payload, _ := json.Marshal(map[string]string{
		"providerID": providerID,
		"modelID":    modelID,
	})

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/summarize", port, req.SessionID)
	log.WithFields(logCtx).WithField("port", port).Info("compacting session via OpenCode API")
	proxyToOpenCode(w, req.Directory, apiURL, payload, logCtx)
}

// handleCalcCost computes an estimated cost for the given token counts using
// the LiteLLM pricing table loaded at startup. Returns zero when the model is
// unknown or the pricing table failed to load.
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

// handleDebugLog accepts a JSON payload from the frontend and prints it to
// stdout via logrus. Intended for environments where the browser devtools
// aren't reachable (iPad, embedded webviews, etc.). The endpoint is
// localhost-gated in server.go to avoid exposing a log sink to the network.
//
// Expected JSON body:
//
//	{
//	  "level": "debug" | "info" | "warn" | "error",  // optional, defaults to info
//	  "message": "<short summary>",                   // required
//	  "data": <any JSON>                              // optional
//	}
//
// The response is always 204 No Content so callers can fire-and-forget.
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
