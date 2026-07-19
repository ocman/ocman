package autoapprove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/ocapi"
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

// JudgeModelSettingKey is the generic-setting key under which the
// user-selected judge model is stored, as a "provider/modelID" string.
const JudgeModelSettingKey = "judge_model"

// loadJudgeModel reads the persisted judge model setting and splits it
// into provider + modelID on the first "/". ok is false when the
// setting is unset or malformed (no "/"), so callers keep the default.
func loadJudgeModel(db judgeModelStore) (provider, modelID string, ok bool) {
	if db == nil {
		return "", "", false
	}
	val, found, err := db.GetSetting(JudgeModelSettingKey)
	if err != nil || !found {
		return "", "", false
	}
	i := strings.IndexByte(val, '/')
	if i <= 0 || i == len(val)-1 {
		return "", "", false
	}
	return val[:i], val[i+1:], true
}

// judgeModelStore is the slice of *state.DB that loadJudgeModel needs,
// kept small so tests can fake it.
type judgeModelStore interface {
	GetSetting(key string) (string, bool, error)
}

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
//
// The three placeholders are:
//  1. Action — OpenCode's human-readable permission summary
//     (e.g. "Bash command").
//  2. Patterns block — the "Patterns:" list (possibly empty).
//  3. Metadata block — the raw tool input (e.g. the actual command
//     for Bash, the file path for Edit). Crucial: without this the
//     judge can't distinguish "mkdir bla" from "rm bla" since both
//     emit the same human-readable Action.
const judgePromptTemplate = `You are a static security reviewer for an AI coding assistant. You must assess a permission request using ONLY the information provided below — do not read any files, run any commands, or use any tools. Answer from the text alone.

## Permission request

Action: %s
%s%s
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
//
// Enabled is a pointer so a missing JSON field (legacy rows persisted
// before this field existed) reads as nil and is treated as enabled.
type PromptSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// judgePrompt formats the full prompt for the given permission request.
// customSections are user-defined rules appended after the built-in
// assessment criteria so the model reads them before deciding.
//
// metadata is the raw tool-input map OpenCode sends alongside every
// permission request. For Bash it carries the actual command; for
// Edit/Write the file path; for Webfetch the URL; etc. Without it the
// judge cannot distinguish "mkdir bla" from "rm bla" — both produce
// the same human-readable `permission` text.
func judgePrompt(permission string, patterns []string, metadata map[string]any, customSections []PromptSection) string {
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
	metadataSection := formatMetadataSection(metadata)
	base := fmt.Sprintf(judgePromptTemplate, permission, patternSection, metadataSection)
	if len(customSections) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	for _, s := range customSections {
		// nil Enabled (legacy rows) counts as enabled; only an
		// explicit false disables the rule.
		if s.Enabled != nil && !*s.Enabled {
			continue
		}
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

// formatMetadataSection renders OpenCode's free-form permission
// metadata as a `Tool input` block. Returns an empty string when
// metadata is nil or empty so the prompt stays clean for tools that
// don't supply any.
//
// Keys are sorted so two calls with the same map produce identical
// prompts (important for testing and for the model not to see
// spurious diffs across runs).
//
// Values are serialised with json.Marshal so strings keep their
// quotes (clear command boundaries) and nested objects/arrays render
// verbatim. Marshal errors fall back to fmt's default formatting.
func formatMetadataSection(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("Tool input:\n")
	for _, k := range keys {
		v := metadata[k]
		b.WriteString("  ")
		b.WriteString(k)
		b.WriteString(": ")
		if encoded, err := json.Marshal(v); err == nil {
			b.Write(encoded)
		} else {
			fmt.Fprintf(&b, "%v", v)
		}
		b.WriteString("\n")
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

	// modelProvider and modelID identify the model used for judgment.
	// Default to the judgeModel* constants; overridden from the
	// persisted "judge_model" setting by backgroundAutoApprove.
	modelProvider string
	modelID       string
}

// newPermissionJudge returns a PermissionJudge wired against the
// real OpenCode port discovery.
func newPermissionJudge(auth ocapi.Auth) *PermissionJudge {
	return &PermissionJudge{
		openCodePort: opencode.DiscoverOpenCodePort,
		httpClient: &http.Client{
			Timeout:   judgeTimeout,
			Transport: auth.Transport(http.DefaultTransport),
		},
		modelProvider: judgeModelProvider,
		modelID:       judgeModelID,
	}
}

// JudgeResult holds the outcome of a single judgment run.
type JudgeResult struct {
	Verdict judgeVerdict
	// SessionID is the transient OpenCode session that hosted the judge
	// conversation. It is deleted by JudgeWithCallback once the verdict
	// has been extracted, so this field is only meaningful inside the
	// callback passed to JudgeWithCallback (onSessionCreated) — by the
	// time JudgeResult is returned, the session no longer exists in
	// OpenCode and this field is always empty.
	//
	// The field is retained so the in-flight "checking" SSE event can
	// still carry the live session ID to clients that might want to
	// observe the judge thinking in real time (no current UI consumer,
	// but the wire format is preserved for future tooling). Persistence
	// of the post-verdict session ID was dropped to avoid the OpenCode
	// DB filling up with one short subagent session per permission.
	SessionID string
	// Reasoning is the one-line conclusion extracted from the model's
	// JSON response (the "reasoning" field). Empty when the model
	// returned no JSON or omitted the field. Surfaced to the UI so
	// users see *why* the judge made its call.
	Reasoning string
}

// JudgeWithCallback decides whether permission is safe to auto-approve
// for the session running in the given directory. The returned
// JudgeResult always has a Verdict; SessionID is non-empty when a judge
// session was successfully created (regardless of verdict), giving the
// caller a link for the user to inspect the reasoning.
//
// metadata is OpenCode's raw tool-input map (e.g. the actual command
// for Bash). Passed verbatim into judgePrompt so the model can see
// the exact action being requested — without it the judge cannot
// distinguish "mkdir bla" from "rm bla".
//
// onSessionCreated is called with the judge session ID as soon as the
// OpenCode session is created — before the prompt is sent and the model
// starts responding. This lets the caller notify connected clients
// immediately so they can show a link. onSessionCreated may be nil.
func (j *PermissionJudge) JudgeWithCallback(ctx context.Context, directory, permission string, patterns []string, metadata map[string]any, customSections []PromptSection, onSessionCreated func(judgeSessionID string)) JudgeResult {
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

	// Always clean up the transient judge session before returning, so
	// each permission check doesn't leave a residual OpenCode session
	// behind. Uses a fresh background context with its own timeout: the
	// caller's ctx may already be cancelled or expired by the time we
	// get here (e.g. user replied manually mid-judge), and the cleanup
	// should still run. Deletion is best-effort — a failure here only
	// leaks one session, never poisons the verdict.
	defer func() {
		delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer delCancel()
		if err := j.deleteSession(delCtx, port, sessionID); err != nil {
			log.WithError(err).WithField("judgeSessionID", sessionID).
				Warn("auto-approve judge: failed to delete judge session")
		}
	}()

	// Notify the caller that the judge session exists. This fires before
	// the prompt is sent so observers can subscribe to the live OpenCode
	// session while the judge is thinking. The session is gone by the
	// time JudgeWithCallback returns, so the SessionID on JudgeResult is
	// always cleared below.
	if onSessionCreated != nil {
		onSessionCreated(sessionID)
	}

	// Send the judge prompt.
	if err := j.sendPrompt(ctx, port, sessionID, permission, patterns, metadata, customSections); err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to send prompt")
		return JudgeResult{Verdict: verdictUnsafe}
	}

	// Stream events until the assistant message finishes, collect text.
	text, err := j.collectResponse(ctx, port, sessionID)
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to collect response")
		return JudgeResult{Verdict: verdictUnsafe}
	}

	verdict, reasoning := parseJudgeResponse(text)
	// SessionID intentionally left empty: the session is being deleted
	// by the deferred cleanup above, so no caller should ever see the
	// post-verdict ID and try to link to it.
	return JudgeResult{Verdict: verdict, Reasoning: reasoning}
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

// deleteSession calls DELETE /session/{id} on the running OpenCode
// instance to remove a transient judge session and all its messages.
// Best-effort: a non-2xx response is reported but does not block the
// caller. Returns an error so the caller can log it.
func (j *PermissionJudge) deleteSession(ctx context.Context, port, sessionID string) error {
	if port == "" || sessionID == "" {
		return nil
	}
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return err
	}
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session: upstream HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// sendPrompt sends the judge prompt to the session.
func (j *PermissionJudge) sendPrompt(ctx context.Context, port, sessionID, permission string, patterns []string, metadata map[string]any, customSections []PromptSection) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{
			{"type": "text", "text": judgePrompt(permission, patterns, metadata, customSections)},
		},
		// model must be a structured object; a raw string is ignored by OpenCode.
		"model": map[string]string{
			"providerID": j.modelProvider,
			"modelID":    j.modelID,
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

// parseJudgeResponse extracts the verdict and one-line reasoning from
// the LLM response text. The expected happy path is a JSON object of
// the form `{"verdict":"safe|unsafe","reasoning":"<one sentence>",...}`;
// if that can't be parsed we fall back to a keyword scan for the
// verdict alone and return an empty reasoning string.
//
// Defaults to verdictUnsafe so an unparseable response forces a human
// to look at the prompt — never silently auto-approves.
func parseJudgeResponse(text string) (judgeVerdict, string) {
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
			Verdict   string `json:"verdict"`
			Reasoning string `json:"reasoning"`
		}
		if err := json.Unmarshal([]byte(jsonText), &obj); err == nil {
			reasoning := strings.TrimSpace(obj.Reasoning)
			switch strings.ToLower(strings.TrimSpace(obj.Verdict)) {
			case "safe":
				return verdictSafe, reasoning
			case "unsafe":
				return verdictUnsafe, reasoning
			}
		}
	}

	// Fallback: keyword scan (handles malformed JSON or plain text).
	// No reasoning available in this path.
	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "UNSAFE") {
		return verdictUnsafe, ""
	}
	if strings.Contains(upper, "SAFE") {
		return verdictSafe, ""
	}

	log.WithField("response", text).Warn("auto-approve judge: could not parse verdict, defaulting to unsafe")
	return verdictUnsafe, ""
}
