package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/NoUseFreak/ocman/internal/db"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// rePortSuffix matches a port number at the end of a string (e.g. ":4096").
var rePortSuffix = regexp.MustCompile(`:(\d+)$`)

//go:embed static/*
var staticFS embed.FS

// Server serves the web UI and API.
type Server struct {
	db   *db.DB
	addr string
}

// New creates a new server.
func New(database *db.DB, addr string) *Server {
	return &Server{db: database, addr: addr}
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/session/", s.handleSession)
	mux.HandleFunc("/api/activity", s.handleActivity)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/hourly", s.handleHourly)
	mux.HandleFunc("/api/session-port/", s.handleSessionPort)
	mux.HandleFunc("/api/send-message", s.handleSendMessage)
	mux.HandleFunc("/api/create-session", s.handleCreateSession)
	mux.HandleFunc("/api/events/", s.handleEvents)

	// Static files with SPA fallback
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to get static subtree: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticContent))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Check if the file exists in static
		f, err := staticContent.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	fmt.Printf("ocman running at http://%s\n", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, stats)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.GetProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	if sessionID == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	messages, err := s.db.GetSessionMessages(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	parts, err := s.db.GetSessionParts(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply pagination to DB results
	totalMessages := len(messages)
	start := len(messages) - offset - limit
	end := len(messages) - offset
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end > len(messages) {
		end = len(messages)
	}
	messages = messages[start:end]

	// Filter parts to only include those for visible messages
	msgIDs := make(map[string]bool)
	for _, m := range messages {
		msgIDs[m.ID] = true
	}
	var filteredParts []db.Part
	for _, p := range parts {
		if msgIDs[p.MessageID] {
			filteredParts = append(filteredParts, p)
		}
	}

	writeJSON(w, map[string]interface{}{
		"session":       session,
		"messages":      messages,
		"parts":         filteredParts,
		"totalMessages": totalMessages,
	})
}

// fetchSessionFromOpenCode tries to get session data from the OpenCode HTTP API.
// Returns the response data and true if successful, or nil and false if not available.
func (s *Server) fetchSessionFromOpenCode(sessionID string, limit, offset int) (map[string]interface{}, bool) {
	// First get session from DB to find the directory
	session, err := s.db.GetSession(sessionID)
	if err != nil {
		return nil, false
	}

	port := discoverOpenCodePort(session.Directory)
	if port == "" {
		return nil, false
	}

	// Fetch session detail and messages in parallel
	var ocSession map[string]interface{}
	var ocMessages []map[string]interface{}
	var sessionErr, messagesErr error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		sessionURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, sessionID)
		resp, err := http.Get(sessionURL)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			sessionErr = fmt.Errorf("failed")
			return
		}
		json.NewDecoder(resp.Body).Decode(&ocSession)
		resp.Body.Close()
	}()

	go func() {
		defer wg.Done()
		messagesURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID)
		resp, err := http.Get(messagesURL)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			messagesErr = fmt.Errorf("failed")
			return
		}
		json.NewDecoder(resp.Body).Decode(&ocMessages)
		resp.Body.Close()
	}()

	wg.Wait()

	if sessionErr != nil || messagesErr != nil || ocSession == nil {
		return nil, false
	}

	// Convert to our format: separate messages and parts
	messages := make([]map[string]interface{}, 0)
	parts := make([]map[string]interface{}, 0)

	for _, m := range ocMessages {
		info, _ := m["info"].(map[string]interface{})
		if info == nil {
			continue
		}

		timeData, _ := info["time"].(map[string]interface{})
		timeCreated := int64(0)
		if tc, ok := timeData["created"].(float64); ok {
			timeCreated = int64(tc)
		}

		msgID, _ := info["id"].(string)
		msgSessionID, _ := info["sessionID"].(string)

		// Remove heavy fields we don't need in the frontend
		delete(info, "summary")
		delete(info, "path")

		msg := map[string]interface{}{
			"id":          msgID,
			"sessionId":   msgSessionID,
			"timeCreated": timeCreated,
			"data":        info,
		}
		messages = append(messages, msg)

		// Extract parts
		if msgParts, ok := m["parts"].([]interface{}); ok {
			for _, p := range msgParts {
				part, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				// Skip non-essential part types
				partType, _ := part["type"].(string)
				if partType == "step-start" || partType == "step-finish" || partType == "snapshot" {
					continue
				}
				// Truncate large outputs to keep response size manageable
				truncatePartOutput(part)
				partEntry := map[string]interface{}{
					"id":        part["id"],
					"messageId": part["messageID"],
					"sessionId": part["sessionID"],
					"data":      part,
				}
				parts = append(parts, partEntry)
			}
		}
	}

	// Compute token totals and duration
	var totalInputTokens, totalOutputTokens float64
	var totalCost float64
	var firstTime, lastTime float64
	for _, m := range messages {
		info, _ := m["data"].(map[string]interface{})
		if info == nil {
			continue
		}
		if t, ok := m["timeCreated"].(int64); ok {
			ft := float64(t)
			if firstTime == 0 || ft < firstTime {
				firstTime = ft
			}
			if ft > lastTime {
				lastTime = ft
			}
		}
		if tokens, ok := info["tokens"].(map[string]interface{}); ok {
			if v, ok := tokens["input"].(float64); ok {
				totalInputTokens += v
			}
			if v, ok := tokens["output"].(float64); ok {
				totalOutputTokens += v
			}
		}
		if c, ok := info["cost"].(float64); ok {
			totalCost += c
		}
	}
	durationMs := int64(0)
	if lastTime > firstTime {
		durationMs = int64(lastTime - firstTime)
	}

	// Apply pagination: return the last `limit` messages starting from `offset` from the end
	totalMessages := len(messages)
	start := len(messages) - offset - limit
	end := len(messages) - offset
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end > len(messages) {
		end = len(messages)
	}
	pagedMessages := messages[start:end]

	// Filter parts to only include those for paged messages
	pagedMsgIDs := make(map[string]bool)
	for _, m := range pagedMessages {
		if id, ok := m["id"].(string); ok {
			pagedMsgIDs[id] = true
		}
	}
	pagedParts := make([]map[string]interface{}, 0)
	for _, p := range parts {
		if mid, ok := p["messageId"].(string); ok && pagedMsgIDs[mid] {
			pagedParts = append(pagedParts, p)
		}
	}

	// Build session object in our format
	timeMap, _ := ocSession["time"].(map[string]interface{})
	summaryMap, _ := ocSession["summary"].(map[string]interface{})

	result := map[string]interface{}{
		"session": map[string]interface{}{
			"id":                ocSession["id"],
			"projectId":         ocSession["projectID"],
			"title":             ocSession["title"],
			"directory":         ocSession["directory"],
			"timeCreated":       timeMap["created"],
			"timeUpdated":       timeMap["updated"],
			"summaryAdditions":  summaryMap["additions"],
			"summaryDeletions":  summaryMap["deletions"],
			"summaryFiles":      summaryMap["files"],
			"messageCount":      totalMessages,
			"durationMs":        durationMs,
			"totalInputTokens":  int64(totalInputTokens),
			"totalOutputTokens": int64(totalOutputTokens),
			"totalCost":         totalCost,
			"hasPort":           true,
			"status":            "done",
		},
		"messages":      pagedMessages,
		"parts":         pagedParts,
		"totalMessages": totalMessages,
	}

	return result, true
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	activity, err := s.db.GetDailyActivity(90)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, activity)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.db.GetModelUsage()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, models)
}

func (s *Server) handleHourly(w http.ResponseWriter, r *http.Request) {
	hourly, err := s.db.GetHourlyActivity()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, hourly)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir parameter required", http.StatusBadRequest)
		return
	}

	port := discoverOpenCodePort(dir)
	if port == "" {
		http.Error(w, "no running OpenCode instance found", http.StatusServiceUnavailable)
		return
	}

	// Connect to the OpenCode SSE endpoint
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/event", port)
	req, err := http.NewRequestWithContext(r.Context(), "GET", apiURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "failed to connect to OpenCode: "+err.Error(), http.StatusBadGateway)
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

	// Proxy the stream line by line
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

const maxOutputLen = 10000

// truncatePartOutput limits the size of tool call outputs and large text
// in a part to prevent massive responses.
func truncatePartOutput(part map[string]interface{}) {
	// Truncate large text content (e.g. file reads)
	if text, ok := part["text"].(string); ok && len(text) > maxOutputLen {
		part["text"] = text[:maxOutputLen] + "\n... (truncated)"
	}

	state, ok := part["state"].(map[string]interface{})
	if !ok {
		return
	}
	// Truncate state.output
	if output, ok := state["output"].(string); ok && len(output) > maxOutputLen {
		state["output"] = output[:maxOutputLen] + "\n... (truncated)"
	}
	// Truncate state.metadata.output
	if meta, ok := state["metadata"].(map[string]interface{}); ok {
		if output, ok := meta["output"].(string); ok && len(output) > maxOutputLen {
			meta["output"] = output[:maxOutputLen] + "\n... (truncated)"
		}
	}
}

// discoverOpenCodePorts returns a map of directory -> port for all running
// OpenCode instances that are listening on TCP ports.
func discoverOpenCodePorts() map[string]string {
	result := make(map[string]string)

	out, err := exec.Command("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return result
	}

	// Parse tabular output to find opencode PIDs and their listen ports.
	// Example line: opencode  91024 dries   15u  IPv4 ... TCP 127.0.0.1:4096 (LISTEN)
	type pidPort struct {
		pid  string
		port string
	}
	var candidates []pidPort
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		if fields[0] != "opencode" {
			continue
		}
		name := fields[len(fields)-2]
		m := rePortSuffix.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		candidates = append(candidates, pidPort{pid: fields[1], port: m[1]})
	}

	// Resolve each candidate's cwd.
	for _, c := range candidates {
		cwdOut, err := exec.Command("lsof", "-a", "-p", c.pid, "-d", "cwd", "-F", "n").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(cwdOut), "\n") {
			if strings.HasPrefix(line, "n/") {
				result[line[1:]] = c.port
				break
			}
		}
	}

	return result
}

// discoverOpenCodePort finds the HTTP port of a running OpenCode instance
// whose working directory matches the given directory.
func discoverOpenCodePort(directory string) string {
	return discoverOpenCodePorts()[directory]
}

func (s *Server) handleSessionPort(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Path[len("/api/session-port/"):]
	if sessionID == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}

	session, err := s.db.GetSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	port := discoverOpenCodePort(session.Directory)
	writeJSON(w, map[string]interface{}{
		"port":      port,
		"available": port != "",
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Directory string `json:"directory"`
	}
	body, err := io.ReadAll(r.Body)
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
		http.Error(w, "no running OpenCode instance found for this directory", http.StatusServiceUnavailable)
		return
	}

	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session", port)
	resp, err := http.Post(apiURL, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		http.Error(w, "failed to reach OpenCode instance: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		http.Error(w, "OpenCode API error: "+string(respBody), resp.StatusCode)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory"`
		Message   string `json:"message"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.Message == "" {
		http.Error(w, "sessionId and message are required", http.StatusBadRequest)
		return
	}

	// Discover the OpenCode instance port for this directory
	port := discoverOpenCodePort(req.Directory)
	if port == "" {
		http.Error(w, "no running OpenCode instance found for this session's directory", http.StatusServiceUnavailable)
		return
	}

	// Proxy the message to the OpenCode HTTP API
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, req.SessionID)
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": req.Message},
		},
	})

	log.Printf("Sending message to session %s via OpenCode API at port %s", req.SessionID, port)

	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("OpenCode API error for session %s: %v", req.SessionID, err)
		http.Error(w, "failed to reach OpenCode instance: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		log.Printf("OpenCode API returned %d for session %s: %s", resp.StatusCode, req.SessionID, string(respBody))
		http.Error(w, "OpenCode API error: "+string(respBody), resp.StatusCode)
		return
	}

	log.Printf("Message sent to session %s via OpenCode API", req.SessionID)
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)
}
