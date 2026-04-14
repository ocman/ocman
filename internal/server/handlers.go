package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
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
	for i := range sessions {
		if _, ok := ports[sessions[i].Directory]; ok {
			sessions[i].HasPort = true
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

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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
	sessionID := r.URL.Path[len("/api/session/"):]
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
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Error("fetching session")
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

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
	contextTokenCount := s.db.GetContextTokenCount(sessionID)
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
	activity, err := s.db.GetDailyActivity(90)
	if err != nil {
		serverError(w, "fetching activity", err)
		return
	}
	writeJSON(w, activity)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.db.GetModelUsage()
	if err != nil {
		serverError(w, "fetching model usage", err)
		return
	}
	writeJSON(w, models)
}

func (s *Server) handleHourly(w http.ResponseWriter, r *http.Request) {
	hourly, err := s.db.GetHourlyActivity()
	if err != nil {
		serverError(w, "fetching hourly activity", err)
		return
	}
	writeJSON(w, hourly)
}

func (s *Server) handleSessionPort(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Path[len("/api/session-port/"):]
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
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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

	body, err := io.ReadAll(io.LimitReader(r.Body, maxSendMessageBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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

	// Discover the OpenCode instance port for this directory
	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "directory": req.Directory}).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
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

	log.WithFields(log.Fields{"sessionID": req.SessionID, "port": port, "images": len(req.Images)}).Info("sending message via OpenCode API")

	resp, err := openCodeClient.Post(apiURL, "application/json", limitedReader(payload))
	if err != nil {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "error": err}).Error("OpenCode API error")
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{"statusCode": resp.StatusCode, "sessionID": req.SessionID, "body": string(respBody)}).Error("OpenCode API error")
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("OpenCode API error (HTTP %d)", resp.StatusCode)
		}
		http.Error(w, errMsg, resp.StatusCode)
		return
	}

	log.WithField("sessionID", req.SessionID).Info("message sent via OpenCode API")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRespondPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID    string `json:"sessionId"`
		Directory    string `json:"directory"`
		PermissionID string `json:"permissionId"`
		Reply        string `json:"reply"`
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if !validateID(req.SessionID) || !validateID(req.PermissionID) {
		http.Error(w, "invalid session or permission ID", http.StatusBadRequest)
		return
	}
	reply := req.Reply
	if reply == "" {
		http.Error(w, "reply is required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "directory": req.Directory}).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
		return
	}

	// OpenCode expects "once", "always", or "reject" as the response value directly.
	switch reply {
	case "once", "always", "reject":
		// valid
	default:
		http.Error(w, "invalid reply value: expected once, always, or reject", http.StatusBadRequest)
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/permissions/%s", port, req.SessionID, req.PermissionID)
	payload, _ := json.Marshal(map[string]interface{}{
		"response": reply,
	})

	resp, err := openCodeClient.Post(apiURL, "application/json", limitedReader(payload))
	if err != nil {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "permissionID": req.PermissionID, "error": err}).Error("OpenCode permission API error")
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{"statusCode": resp.StatusCode, "sessionID": req.SessionID, "permissionID": req.PermissionID, "body": string(respBody)}).Error("OpenCode permission API error")
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("OpenCode permission API error (HTTP %d)", resp.StatusCode)
		}
		http.Error(w, errMsg, resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRespondQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string     `json:"sessionId"`
		Directory string     `json:"directory"`
		RequestID string     `json:"requestId"`
		Answers   [][]string `json:"answers"`
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.RequestID == "" {
		http.Error(w, "sessionId and requestId are required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "directory": req.Directory}).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/question/%s/reply", port, req.RequestID)
	payload, _ := json.Marshal(map[string]interface{}{
		"answers": req.Answers,
	})

	resp, err := openCodeClient.Post(apiURL, "application/json", limitedReader(payload))
	if err != nil {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "requestID": req.RequestID, "error": err}).Error("OpenCode question reply API error")
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{"statusCode": resp.StatusCode, "sessionID": req.SessionID, "requestID": req.RequestID, "body": string(respBody)}).Error("OpenCode question reply API error")
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("OpenCode question API error (HTTP %d)", resp.StatusCode)
		}
		http.Error(w, errMsg, resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRejectQuestion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
		RequestID string `json:"requestId"`
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.RequestID == "" {
		http.Error(w, "sessionId and requestId are required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "directory": req.Directory}).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/question/%s/reject", port, req.RequestID)

	resp, err := openCodeClient.Post(apiURL, "application/json", limitedReader([]byte("{}")))
	if err != nil {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "requestID": req.RequestID, "error": err}).Error("OpenCode question reject API error")
		http.Error(w, "failed to reach OpenCode instance", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.WithFields(log.Fields{"statusCode": resp.StatusCode, "sessionID": req.SessionID, "requestID": req.RequestID, "body": string(respBody)}).Error("OpenCode question reject API error")
		errMsg := string(respBody)
		if errMsg == "" {
			errMsg = fmt.Sprintf("OpenCode question API error (HTTP %d)", resp.StatusCode)
		}
		http.Error(w, errMsg, resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
