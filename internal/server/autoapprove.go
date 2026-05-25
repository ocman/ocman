package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	opencode "github.com/NoUseFreak/ocman/internal/platforms/opencode"
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

// judgePrompt formats the full prompt for the given permission request.
func judgePrompt(permission string, patterns []string) string {
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
	return fmt.Sprintf(judgePromptTemplate, permission, patternSection)
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
func (j *PermissionJudge) Judge(ctx context.Context, directory, permission string, patterns []string) JudgeResult {
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
	if err := j.sendPrompt(ctx, port, sessionID, permission, patterns); err != nil {
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
func (j *PermissionJudge) sendPrompt(ctx context.Context, port, sessionID, permission string, patterns []string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": judgePrompt(permission, patterns)},
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
