package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	opencode "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

// judgeVerdict is the result of the auto-approve judge.
type judgeVerdict string

const (
	verdictSafe   judgeVerdict = "safe"
	verdictUnsafe judgeVerdict = "unsafe"
)

// judgeTimeout caps how long we wait for the LLM judge to respond.
// Chosen to be long enough for a fast model (haiku-class) while still
// being short enough that the user doesn't wait too long if the judge
// hangs.
const judgeTimeout = 30 * time.Second

// judgeModelProvider and judgeModelID identify the model used for judgment.
// Sent as a structured object matching OpenCode's prompt_async schema.
const judgeModelProvider = "anthropic"
const judgeModelID = "claude-haiku-4-5"

// judgeAgent is the OpenCode agent used for the judge session.
// "build" is the standard default agent; it has all tools available
// but the prompt explicitly instructs the model not to use them.
const judgeAgent = "build"

// judgePromptTemplate is the full message sent to the model. It asks for
// structured JSON so the session shows readable reasoning and the
// verdict is unambiguous to parse.
//
// The response schema is intentionally flat so the model is unlikely
// to wrap it in markdown fences or add extra fields:
//
//	{
//	  "verdict": "safe" | "unsafe",
//	  "reasoning": "<one or two sentences>",
//	  "risk_factors": ["..."]          // empty when safe
//	}
const judgePromptTemplate = `You are a static security reviewer for an AI coding assistant. You must assess a permission request using ONLY the information provided below — do not read any files, run any commands, or use any tools. Answer from the text alone.

## Permission request

Action: %s
%s
## Assessment criteria

**SAFE** — the action is read-only and non-destructive, and the paths do not include files that commonly contain secrets or credentials.

**UNSAFE** — the action could write, delete, or execute; or the paths include files that commonly hold secrets (e.g. .env, *.key, *.pem, id_rsa, credentials, ~/.ssh/, ~/.gnupg/, config with tokens/passwords).

When in doubt, respond unsafe.

## Response format

Reply with valid JSON only — no markdown fences, no extra text:

{"verdict":"safe","reasoning":"<one sentence>","risk_factors":[]}

or

{"verdict":"unsafe","reasoning":"<one sentence>","risk_factors":["<reason1>","<reason2>"]}`

// PromptSection is a user-defined extra section appended to the judge prompt.
// Sent from the frontend settings page; each section is rendered as
// "## <Title>\n<Content>" below the built-in assessment criteria.
type PromptSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// judgePrompt formats the full prompt for the given permission request.
// customSections are user-defined rules appended after the built-in
// assessment criteria so the model reads them before deciding.
func judgePrompt(permission string, patterns []string, customSections []PromptSection) string {
	var patternSection string
	if len(patterns) > 0 {
		var b strings.Builder
		b.WriteString("Patterns:\n")
		for _, p := range patterns {
			b.WriteString("  - ")
			b.WriteString(p)
			b.WriteString("\n")
		}
		patternSection = b.String()
	}
	base := fmt.Sprintf(judgePromptTemplate, permission, patternSection)
	if len(customSections) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	for _, s := range customSections {
		title := strings.TrimSpace(s.Title)
		content := strings.TrimSpace(s.Content)
		if title == "" && content == "" {
			continue
		}
		b.WriteString("\n\n## ")
		if title != "" {
			b.WriteString(title)
		} else {
			b.WriteString("Additional rule")
		}
		b.WriteString("\n")
		b.WriteString(content)
	}
	return b.String()
}

// PermissionJudge runs an LLM via a running OpenCode instance to
// decide whether a permission request is safe to auto-approve.
//
// It creates a temporary OpenCode session, sends the judge prompt,
// streams the SSE response until the assistant message completes,
// parses SAFE/UNSAFE from the text, then abandons the session.
// On any error it returns verdictUnsafe so the permission falls
// through to human review.
type PermissionJudge struct {
	// openCodePort is a function that returns the HTTP port for the
	// OpenCode instance managing the given session directory.
	// Injected so tests can provide a fake OpenCode server.
	openCodePort func(directory string) string

	// httpClient is used for all calls to the OpenCode HTTP API.
	// Defaults to a client with judgeTimeout.
	httpClient *http.Client
}

// newPermissionJudge returns a PermissionJudge wired against the
// real OpenCode port discovery.
func newPermissionJudge() *PermissionJudge {
	return &PermissionJudge{
		openCodePort: opencode.DiscoverOpenCodePort,
		httpClient: &http.Client{
			Timeout: judgeTimeout,
		},
	}
}

// JudgeResult holds the outcome of a single judgment run.
type JudgeResult struct {
	Verdict   judgeVerdict
	// SessionID is the OpenCode session that hosted the judge conversation.
	// It is kept alive so the user can navigate to it and read the reasoning.
	// Empty when the judge could not start (e.g. no running OpenCode instance).
	SessionID string
}

// Judge decides whether permission is safe to auto-approve for the
// session running in the given directory. The returned JudgeResult
// always has a Verdict; SessionID is non-empty when a judge session
// was successfully created (regardless of verdict), giving the caller
// a link for the user to inspect the reasoning.
func (j *PermissionJudge) Judge(ctx context.Context, directory, permission string, patterns []string, customSections []PromptSection) JudgeResult {
	if j == nil || j.openCodePort == nil {
		return JudgeResult{Verdict: verdictUnsafe}
	}
	ctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()

	port := j.openCodePort(directory)
	if port == "" {
		log.WithField("directory", directory).
			Warn("auto-approve judge: no running OpenCode instance found, falling through to human")
		return JudgeResult{Verdict: verdictUnsafe}
	}

	// Title matches the subagent pattern so the default session-list
	// filter hides it (titles like "%(% subagent)" are excluded).
	sessionID, err := j.createSession(ctx, port, directory, "(auto-approve subagent)")
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to create judge session")
		return JudgeResult{Verdict: verdictUnsafe}
	}
	// The session is intentionally kept alive after judgment so the user
	// can navigate to it and read the model's reasoning.

	// Send the judge prompt.
	if err := j.sendPrompt(ctx, port, sessionID, permission, patterns, customSections); err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to send prompt")
		return JudgeResult{Verdict: verdictUnsafe, SessionID: sessionID}
	}

	// Stream events until the assistant message finishes, collect text.
	text, err := j.collectResponse(ctx, port, sessionID)
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to collect response")
		return JudgeResult{Verdict: verdictUnsafe, SessionID: sessionID}
	}

	return JudgeResult{Verdict: parseVerdict(text), SessionID: sessionID}
}

// createSession creates a new OpenCode session in the given directory,
// sets the given title, and returns the session ID.
func (j *PermissionJudge) createSession(ctx context.Context, port, directory, title string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"directory": directory})
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("create session: upstream HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == "" {
		return "", fmt.Errorf("create session: could not parse session ID from response")
	}

	// Set the title so the session is identifiable in the sidebar.
	if title != "" {
		titlePayload, _ := json.Marshal(map[string]string{"title": title})
		titleURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, parsed.ID)
		titleReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, titleURL, bytes.NewReader(titlePayload))
		if err == nil {
			titleReq.Header.Set("Content-Type", "application/json")
			titleResp, err := j.httpClient.Do(titleReq)
			if err == nil {
				titleResp.Body.Close()
			}
		}
		// Title setting is best-effort — don't fail the whole judge if it doesn't work.
	}

	return parsed.ID, nil
}

// sendPrompt sends the judge prompt to the session.
func (j *PermissionJudge) sendPrompt(ctx context.Context, port, sessionID, permission string, patterns []string, customSections []PromptSection) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": judgePrompt(permission, patterns, customSections)},
		},
		// model must be a structured object; a raw string is ignored by OpenCode.
		"model": map[string]string{
			"providerID": judgeModelProvider,
			"modelID":    judgeModelID,
		},
		// agent is required for OpenCode to dispatch to the model.
		"agent": judgeAgent,
	})
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/prompt_async", port, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send prompt: upstream HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// recentUserMessagesLimit is the number of recent messages to fetch
// when building the user-intent context for the judge prompt.
const recentUserMessagesLimit = 6

// recentUserMessages fetches the last N messages from a running
// OpenCode session and returns the plain text of user-role messages.
// Returns nil when the fetch fails so callers can proceed without context.
func (j *PermissionJudge) recentUserMessages(ctx context.Context, port, sessionID string) []string {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil
	}
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}

	var messages []map[string]interface{}
	if err := json.Unmarshal(body, &messages); err != nil {
		return nil
	}

	var out []string
	for _, m := range messages {
		info, _ := m["info"].(map[string]interface{})
		if info == nil {
			continue
		}
		if role, _ := info["role"].(string); role != "user" {
			continue
		}
		if txt := extractTextFromParts(m); txt != "" {
			out = append(out, txt)
		}
	}
	// Return only the most recent N messages to keep the prompt concise.
	if len(out) > recentUserMessagesLimit {
		out = out[len(out)-recentUserMessagesLimit:]
	}
	return out
}

// pollInterval is the delay between message-list polls while waiting
// for the judge model to finish its response.
const pollInterval = 500 * time.Millisecond

// collectResponse polls GET /session/{id}/message until the assistant
// message has a non-empty "finish" field, then returns the concatenated
// text content. This is more reliable than the global SSE /event stream,
// which is a broadcast that can miss events emitted before we connect.
func (j *PermissionJudge) collectResponse(ctx context.Context, port, sessionID string) (string, error) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return "", err
		}
		resp, err := j.httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("fetching messages: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("reading messages: %w", err)
		}
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("messages endpoint HTTP %d", resp.StatusCode)
		}

		var messages []map[string]interface{}
		if err := json.Unmarshal(body, &messages); err != nil {
			return "", fmt.Errorf("decoding messages: %w", err)
		}

		// Find the last assistant message and check if it has finished.
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]
			info, _ := msg["info"].(map[string]interface{})
			if info == nil {
				continue
			}
			role, _ := info["role"].(string)
			if role != "assistant" {
				continue
			}
			finish, _ := info["finish"].(string)
			if finish == "" {
				break // still running — stop scanning, wait and retry
			}
			// Message finished — extract text from parts.
			return extractTextFromParts(msg), nil
		}

		// Not done yet — wait before polling again.
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// extractTextFromParts walks a raw OpenCode message's "parts" array and
// returns the concatenated text content of all text-type parts.
func extractTextFromParts(msg map[string]interface{}) string {
	parts, _ := msg["parts"].([]interface{})
	var b strings.Builder
	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if pt, _ := part["type"].(string); pt != "text" {
			continue
		}
		if txt, _ := part["text"].(string); txt != "" {
			b.WriteString(txt)
		}
	}
	return b.String()
}


// writeSSEEvent writes a single named SSE event to w and calls flush if
// non-nil. This is used by backgroundAutoApprove to push synthetic
// ocman-originated events (e.g. permission.checking, permission.auto-approved)
// back to connected browser clients through the proxied event stream.
func writeSSEEvent(w io.Writer, flush func(), eventType string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(data))
	if flush != nil {
		flush()
	}
}

// --- Background (server-side) auto-approve ---

// ssePermissionTee wraps an io.Writer and tees the SSE byte stream to a
// side-channel that parses permission.asked events. When one is seen,
// onPermission is called in a new goroutine so the main write path is
// never blocked.
//
// Parsing is line-based: SSE lines are terminated by '\n'. We buffer
// across Read boundaries so events split across multiple Write calls
// are still detected. The tee is intentionally lossy on parse errors —
// it always forwards every byte to the underlying writer unchanged.
type ssePermissionTee struct {
	w            io.Writer
	flush        func()
	buf          []byte
	mu           sync.Mutex
	onPermission func(permissionID, permission string, patterns []string)
}

func (t *ssePermissionTee) Write(p []byte) (int, error) {
	// Always forward bytes to the real writer first.
	n, err := t.w.Write(p)

	// Parse in the background, never blocking the response writer.
	t.mu.Lock()
	t.buf = append(t.buf, p[:n]...)
	t.mu.Unlock()

	go t.drain()

	return n, err
}

// drain processes all complete SSE events currently in the buffer.
// Called in a goroutine so it never holds up the HTTP write path.
func (t *ssePermissionTee) drain() {
	t.mu.Lock()
	data := t.buf
	t.mu.Unlock()

	scanner := bufio.NewScanner(bytes.NewReader(data))

	var (
		eventType string
		dataLines []string
		consumed  int
	)

	rawLines := splitSSELines(data)
	pos := 0
	for _, line := range rawLines {
		lineLen := len(line) + 1 // +1 for '\n'
		if pos+lineLen > len(data) {
			break
		}
		raw := string(line)

		switch {
		case raw == "":
			// Blank line = end of event. Dispatch if we have data.
			if eventType != "" && len(dataLines) > 0 {
				t.dispatchEvent(eventType, strings.Join(dataLines, "\n"))
			}
			eventType = ""
			dataLines = dataLines[:0]
			consumed = pos + lineLen

		case strings.HasPrefix(raw, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(raw, "event:"))

		case strings.HasPrefix(raw, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(raw, "data:")))
		}

		pos += lineLen
	}
	_ = scanner // suppress unused warning from import

	// Trim the consumed prefix from the buffer.
	if consumed > 0 {
		t.mu.Lock()
		if consumed <= len(t.buf) {
			t.buf = t.buf[consumed:]
		}
		t.mu.Unlock()
	}
}

// splitSSELines splits b into lines (split on '\n'), omitting the
// terminator. Lines whose terminator has not yet arrived are excluded
// (they remain in the buffer for the next drain pass).
func splitSSELines(b []byte) [][]byte {
	var lines [][]byte
	for {
		idx := bytes.IndexByte(b, '\n')
		if idx < 0 {
			break
		}
		line := b[:idx]
		// Trim '\r' for \r\n line endings.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		b = b[idx+1:]
	}
	return lines
}

// dispatchEvent is called when a complete SSE event has been parsed.
// It fires onPermission if the event type is "permission.asked" and
// the payload carries the expected fields.
func (t *ssePermissionTee) dispatchEvent(eventType, dataJSON string) {
	if eventType != "permission.asked" {
		return
	}
	var props struct {
		ID         string   `json:"id"`
		Permission string   `json:"permission"`
		Patterns   []string `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
		return
	}
	if props.ID == "" || props.Permission == "" {
		return
	}
	go t.onPermission(props.ID, props.Permission, props.Patterns)
}

// backgroundAutoApprove is the authoritative auto-approve engine.
// It fires whenever an SSE permission.asked event is observed on a
// proxied event stream — even when no browser tab has the session open.
//
// When auto-approve is enabled for the session it:
//  1. Emits an "ocman.permission.checking" SSE event to any connected
//     clients so the UI can show a "checking" indicator immediately.
//  2. Loads the user-defined judge prompt sections from stateDB.
//  3. Runs the LLM judge.
//  4. If the verdict is SAFE, responds "once" directly to the running
//     OpenCode instance, persists the approval, and emits an
//     "ocman.permission.auto-approved" SSE event back to clients.
//
// sseW and sseFlush are the response writer and flusher for the
// currently-connected SSE client. Both may be nil when the function
// is called without a live client (e.g. in tests).
//
// This function blocks (it calls judge.Judge which polls OpenCode) and
// must always be called in a goroutine.
func (s *Server) backgroundAutoApprove(
	ctx context.Context,
	platformID platforms.ID,
	adapter platforms.Platform,
	sessionID string,
	permissionID string,
	permission string,
	patterns []string,
	sseW io.Writer,
	sseFlush func(),
) {
	logger := log.WithFields(log.Fields{
		"sessionID":    sessionID,
		"permissionID": permissionID,
	})

	// Check auto-approve state: per-session override, then server default.
	enabled := s.autoApproveDefault
	if s.stateDB != nil {
		if perSession, exists, err := s.stateDB.GetAutoApprove(string(platformID), sessionID); err == nil && exists {
			enabled = perSession
		}
	}
	if !enabled {
		return
	}

	// Resolve directory for port discovery.
	if s.db == nil {
		logger.Warn("background auto-approve: no OpenCode DB, cannot resolve session directory")
		return
	}
	dbSession, err := s.db.GetSession(sessionID)
	if err != nil || dbSession == nil {
		logger.WithError(err).Warn("background auto-approve: session not found in DB")
		return
	}

	// Notify connected clients that the judge is running.
	if sseW != nil {
		checkingPayload, _ := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionId":    sessionID,
		})
		writeSSEEvent(sseW, sseFlush, "ocman.permission.checking", checkingPayload)
	}

	logger.Info("background auto-approve: judging permission")

	// Load user-defined prompt sections from stateDB so headless runs
	// use the same custom rules as the settings page.
	var customSections []PromptSection
	if s.stateDB != nil {
		if stored, err := s.stateDB.GetPromptSections(); err == nil {
			for _, ps := range stored {
				customSections = append(customSections, PromptSection{
					Title:   ps.Title,
					Content: ps.Content,
				})
			}
		}
	}

	// Build a user-intent section from recent messages in this session
	// so the judge can factor in what the user explicitly asked for.
	if s.judge != nil && s.judge.openCodePort != nil {
		port := s.judge.openCodePort(dbSession.Directory)
		if port != "" {
			msgs := s.judge.recentUserMessages(ctx, port, sessionID)
			if len(msgs) > 0 {
				var b strings.Builder
				b.WriteString("The user recently sent these messages (oldest first):\n")
				for _, m := range msgs {
					b.WriteString("  - ")
					// Truncate very long messages to keep the prompt concise.
					if len(m) > 300 {
						m = m[:300] + "…"
					}
					b.WriteString(m)
					b.WriteString("\n")
				}
				b.WriteString("\nIf the permission request is a direct and proportionate consequence of what the user asked for, lean toward SAFE.")
				customSections = append(customSections, PromptSection{
					Title:   "Recent user intent",
					Content: b.String(),
				})
			}
		}
	}

	result := s.judge.Judge(ctx, dbSession.Directory, permission, patterns, customSections)

	logger.WithFields(log.Fields{
		"verdict":        string(result.Verdict),
		"judgeSessionID": result.SessionID,
	}).Info("background auto-approve: judge returned")

	if result.Verdict != verdictSafe {
		return
	}

	// Respond "once" to the pending permission.
	if err := adapter.RespondPermission(ctx, platforms.RespondPermissionRequest{
		SessionID:    sessionID,
		PermissionID: permissionID,
		Reply:        "once",
	}); err != nil {
		logger.WithError(err).Warn("background auto-approve: failed to respond to permission")
		return
	}

	approvedAt := time.Now().UnixMilli()

	// Persist the approval so the UI notice survives a page refresh.
	if s.stateDB != nil {
		if err := s.stateDB.RecordApprovedPermission(
			string(platformID),
			sessionID,
			state.ApprovedPermission{
				PermissionID:   permissionID,
				PermissionText: permission,
				Patterns:       patterns,
				JudgeSessionID: result.SessionID,
				ApprovedAt:     approvedAt,
			},
		); err != nil {
			logger.WithError(err).Warn("background auto-approve: failed to persist approval")
		}
	}

	// Notify connected clients so they can inject the notice immediately
	// without waiting for a page reload.
	if sseW != nil {
		if patterns == nil {
			patterns = []string{}
		}
		approvedPayload, _ := json.Marshal(map[string]interface{}{
			"permissionId":   permissionID,
			"sessionId":      sessionID,
			"permission":     permission,
			"patterns":       patterns,
			"judgeSessionId": result.SessionID,
			"approvedAt":     approvedAt,
		})
		writeSSEEvent(sseW, sseFlush, "ocman.permission.auto-approved", approvedPayload)
	}

	logger.Info("background auto-approve: permission approved")
}

// parseVerdict extracts the verdict from the LLM response text.
// Tries to parse a JSON object with a "verdict" field first; falls
// back to keyword scanning for robustness. Defaults to verdictUnsafe.
func parseVerdict(text string) judgeVerdict {
	trimmed := strings.TrimSpace(text)

	// Try JSON parse — expected happy path.
	// The model may wrap with markdown fences; strip them first.
	jsonText := trimmed
	if strings.HasPrefix(jsonText, "```") {
		if end := strings.LastIndex(jsonText, "```"); end > 3 {
			jsonText = strings.TrimSpace(jsonText[strings.Index(jsonText, "\n")+1 : end])
		}
	}
	// Find the first '{' in case there's any leading text.
	if start := strings.IndexByte(jsonText, '{'); start >= 0 {
		jsonText = jsonText[start:]
		if end := strings.LastIndexByte(jsonText, '}'); end >= 0 {
			jsonText = jsonText[:end+1]
		}
		var obj struct {
			Verdict string `json:"verdict"`
		}
		if err := json.Unmarshal([]byte(jsonText), &obj); err == nil {
			switch strings.ToLower(strings.TrimSpace(obj.Verdict)) {
			case "safe":
				return verdictSafe
			case "unsafe":
				return verdictUnsafe
			}
		}
	}

	// Fallback: keyword scan (handles malformed JSON or plain text).
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "UNSAFE") {
		return verdictUnsafe
	}
	if strings.Contains(upper, "SAFE") {
		return verdictSafe
	}

	log.WithField("response", text).Warn("auto-approve judge: could not parse verdict, defaulting to unsafe")
	return verdictUnsafe
}
