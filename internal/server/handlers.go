package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

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

	// Discover all running OpenCode ports once and mark sessions.
	ports := discoverOpenCodePorts()
	for i := range sessions {
		if _, ok := ports[sessions[i].Directory]; ok {
			sessions[i].HasPort = true
		}
	}

	writeJSON(w, sessions)
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

	writeJSON(w, map[string]interface{}{
		"session":       session,
		"messages":      pagedMessages,
		"parts":         filteredParts,
		"totalMessages": totalMessages,
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

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
		Message   string `json:"message"`
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
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	// Discover the OpenCode instance port for this directory
	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		log.WithFields(log.Fields{"sessionID": req.SessionID, "directory": req.Directory}).Warn("no running OpenCode instance found")
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
		return
	}

	// Use prompt_async so the request returns immediately (204) and the
	// assistant response streams back via SSE instead of blocking until
	// the full response is generated.
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/prompt_async", port, req.SessionID)
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": req.Message},
		},
	})

	log.WithFields(log.Fields{"sessionID": req.SessionID, "port": port}).Info("sending message via OpenCode API")

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
		http.Error(w, "OpenCode API error", resp.StatusCode)
		return
	}

	log.WithField("sessionID", req.SessionID).Info("message sent via OpenCode API")
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
