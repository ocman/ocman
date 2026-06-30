package server

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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

// --- Per-session SSE writer registry ---
//
// Active SSE connections register their writer here so non-SSE code paths
// (REST permission listing, prompt resurrection) can push synthetic
// ocman.permission.* events into the same connection.

// registerSseSink creates and records an sseSink for sessionID. The
// returned pointer must be passed to unregisterSseSink when the
// connection terminates so the sink is closed (any in-flight or
// future writes turn into no-ops, preventing panics on a recycled
// http.ResponseWriter).
//
// If a sink was already registered for the same sessionID (rare —
// second tab on the same session) the previous one is closed; the
// older client will simply stop receiving ocman.* events but its
// proxied OpenCode events continue unaffected.
func (s *Server) registerSseSink(sessionID string, w io.Writer, flush func()) *sseSink {
	if s == nil {
		return nil
	}
	sink := &sseSink{w: w, flush: flush}
	s.sseSessionsMu.Lock()
	if s.sseSessions == nil {
		s.sseSessions = make(map[string]*sseSink)
	}
	prev := s.sseSessions[sessionID]
	s.sseSessions[sessionID] = sink
	s.sseSessionsMu.Unlock()
	if prev != nil {
		prev.close()
	}
	return sink
}

// unregisterSseSink closes the sink (so any in-flight or future writes
// become no-ops) and removes it from the registry, but only if it
// still matches the one being closed. This avoids clobbering a newer
// tab's registration when an old SSE connection finally tears down.
func (s *Server) unregisterSseSink(sessionID string, sink *sseSink) {
	if s == nil || sink == nil {
		return
	}
	s.sseSessionsMu.Lock()
	if cur, ok := s.sseSessions[sessionID]; ok && cur == sink {
		delete(s.sseSessions, sessionID)
	}
	s.sseSessionsMu.Unlock()
	sink.close()
}

// lookupSseSink returns the registered sink for sessionID, or nil if
// none. The returned pointer is stable — closing it is safe even after
// the registry entry has been removed or replaced.
func (s *Server) lookupSseSink(sessionID string) *sseSink {
	if s == nil {
		return nil
	}
	s.sseSessionsMu.Lock()
	defer s.sseSessionsMu.Unlock()
	return s.sseSessions[sessionID]
}

// --- Auto-approve per-permission state tracking ---

// autoApproveStatus is the per-permission state remembered for the
// lifetime of the ocman process. A non-nil cancel means a judge
// goroutine is still running; a non-empty verdict means the judge
// finished. The two are mutually exclusive only in steady state — a
// running judge transitions from (cancel non-nil, verdict "") to
// (cancel nil, verdict non-empty) when recordJudged is called.
//
// judgeStartsAt and checking exist so a freshly-connected SSE sink
// (the headless-watcher case where the watcher claimed the permission
// before the frontend was open) can replay the most recent applicable
// ocman.permission.* event when ensureAutoApprove short-circuits.
type autoApproveStatus struct {
	// cancel cancels the judge goroutine's context. Non-nil while the
	// goroutine is running; cleared by releaseAutoApprove.
	cancel context.CancelFunc

	// judgeStartsAt is the wall-clock Unix-ms at which the judge will
	// start running (i.e. now + configured delay). Used to replay
	// ocman.permission.pending with a stable countdown anchor when a
	// frontend connects mid-delay.
	judgeStartsAt int64

	// checking indicates whether the configured delay has elapsed and
	// the judge is actually running. Toggled by markAutoApproveChecking
	// after the delay sleep finishes.
	checking bool

	// verdict is the final verdict once the judge has finished. Empty
	// while the judge is still running.
	verdict judgeVerdict

	// reasoning is the one-line conclusion extracted from the judge's
	// JSON response. Populated when verdict is non-empty. Surfaced to
	// the UI on the ocman.permission.flagged event so the user sees
	// *why* the judge made its call.
	reasoning string
}

// autoApproveKey is the registry key for a single permission record.
func autoApproveKey(sessionID, permissionID string) string {
	return sessionID + "|" + permissionID
}

// claimAutoApprove atomically registers a new judge run for
// (sessionID, permissionID) and returns a cancellable context derived
// from parent. The second return value is true if the claim was
// granted; false if another goroutine is already handling this
// permission, OR a verdict for it has already been recorded.
//
// Callers that already know the judgeStartsAt anchor (the standard
// ensureAutoApprove path) should use claimAutoApproveWithStart so the
// anchor is stored for later replay. This helper exists for symmetry
// with the pre-watcher API and defaults judgeStartsAt to 0.
func (s *Server) claimAutoApprove(parent context.Context, sessionID, permissionID string) (context.Context, bool) {
	return s.claimAutoApproveWithStart(parent, sessionID, permissionID, 0)
}

// claimAutoApproveWithStart is claimAutoApprove plus a judgeStartsAt
// anchor. The anchor is stored on the status record so a later
// short-circuit replay can re-emit ocman.permission.pending with the
// same value, letting the frontend resume the countdown without a
// fresh clock.
func (s *Server) claimAutoApproveWithStart(parent context.Context, sessionID, permissionID string, judgeStartsAt int64) (context.Context, bool) {
	if s == nil {
		// Nil-Server only happens in test setups that don't exercise
		// cancellation; return the parent unchanged.
		return parent, true
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	if existing := s.autoApprove[key]; existing != nil {
		// Either still in flight or already judged — either way the
		// caller must not start a second goroutine. Replay logic in
		// ensureAutoApprove handles the bring-up of any newly-connected
		// sink.
		return nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	s.autoApprove[key] = &autoApproveStatus{
		cancel:        cancel,
		judgeStartsAt: judgeStartsAt,
	}
	return ctx, true
}

// markAutoApproveChecking flips the status's checking flag, signalling
// that the configured delay has elapsed and the judge is now running.
// Used by the replay path to choose between emitting
// ocman.permission.pending (still waiting) and .checking (judge
// active). No-op if no status exists for the permission.
func (s *Server) markAutoApproveChecking(sessionID, permissionID string) {
	if s == nil {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	if st := s.autoApprove[key]; st != nil {
		st.checking = true
	}
	s.autoApproveMu.Unlock()
}

// releaseAutoApprove invokes the registered cancel func and clears it
// on the status record. The status itself is retained so a later
// ensureAutoApprove call for the same permissionID can still replay
// the recorded verdict (or, for an in-flight goroutine that exits
// before a verdict is recorded, recognise that the slot is free for
// a fresh claim).
//
// Idempotent — safe to call after cancelAutoApprove. Must be called by
// the goroutine that successfully claimed the entry, typically in a
// deferred block.
func (s *Server) releaseAutoApprove(sessionID, permissionID string) {
	if s == nil {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	st := s.autoApprove[key]
	if st == nil {
		s.autoApproveMu.Unlock()
		return
	}
	cancel := st.cancel
	st.cancel = nil
	// If the judge exited without recording a verdict (cancelled
	// before completion, panic, etc.) drop the record so a fresh
	// permission with the same key can claim again. Recorded
	// verdicts are kept so REST resurrection can replay them.
	if st.verdict == "" {
		delete(s.autoApprove, key)
	}
	s.autoApproveMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// cancelAutoApprove signals the in-flight judge for (sessionID,
// permissionID) to abort. No-op if there is no judge running. The
// goroutine sees ctx.Done() at its next select point and drops the
// result. The status record is left in place — releaseAutoApprove
// (from the goroutine's defer) is what evicts it. This way two
// cancels in quick succession don't fight a re-entry; the slot only
// frees when the goroutine actually exits.
//
// Returns true if a cancel was sent (something was running), false
// if there was nothing to cancel.
func (s *Server) cancelAutoApprove(sessionID, permissionID string) bool {
	if s == nil {
		return false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	st := s.autoApprove[key]
	var cancel context.CancelFunc
	if st != nil {
		cancel = st.cancel
	}
	s.autoApproveMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// recordJudged persists the verdict for (sessionID, permissionID) so a
// later ensureAutoApprove call for the same OpenCode permission ID
// short-circuits instead of running the judge again. Called by
// backgroundAutoApprove on every terminal path (safe or unsafe).
//
// Equivalent to recordJudgedWithReasoning with reasoning="". Retained
// so call-sites that don't have reasoning handy stay readable.
func (s *Server) recordJudged(sessionID, permissionID string, verdict judgeVerdict) {
	s.recordJudgedWithReasoning(sessionID, permissionID, verdict, "")
}

// recordJudgedWithReasoning is recordJudged plus the one-line reasoning
// extracted from the judge's response. Surfaced to the UI on the
// ocman.permission.flagged event during replay so users see *why* the
// judge said unsafe.
func (s *Server) recordJudgedWithReasoning(sessionID, permissionID string, verdict judgeVerdict, reasoning string) {
	if s == nil || permissionID == "" {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	st := s.autoApprove[key]
	if st == nil {
		st = &autoApproveStatus{}
		s.autoApprove[key] = st
	}
	st.verdict = verdict
	st.reasoning = reasoning
	s.autoApproveMu.Unlock()
}

// lookupJudged returns the cached verdict for (sessionID, permissionID)
// and ok=true if the judge already produced a verdict in this process.
// Pure read of the verdict field; the status may still be in flight if
// verdict is empty (in which case ok is false).
func (s *Server) lookupJudged(sessionID, permissionID string) (judgeVerdict, bool) {
	if s == nil {
		return "", false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	st := s.autoApprove[key]
	if st == nil || st.verdict == "" {
		return "", false
	}
	return st.verdict, true
}

// lookupAutoApproveStatus returns a snapshot copy of the current status
// for (sessionID, permissionID) and ok=true if any state is recorded
// (in-flight OR judged). The returned struct is a value copy so callers
// can read fields without holding the mutex. Returns the zero status
// and ok=false when no record exists.
func (s *Server) lookupAutoApproveStatus(sessionID, permissionID string) (autoApproveStatus, bool) {
	if s == nil {
		return autoApproveStatus{}, false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	st := s.autoApprove[key]
	if st == nil {
		return autoApproveStatus{}, false
	}
	return *st, true
}

// --- Per-session safe-command cache ---
//
// The autoApprove map above caches verdicts by the OpenCode-generated
// permissionID, so resurrecting *the same prompt* short-circuits the
// judge. But every fresh permission for the same logical action (e.g.
// the user running `pnpm test` five times in a row) gets a new
// permissionID, so the user pays for the judge each time.
//
// The safe-command cache fills that gap: when the judge returns "safe"
// for a Bash command, we additionally remember the verdict keyed by
// md5(metadata["command"]) inside the session. The next time the same
// raw command shows up — even with a different permissionID — we skip
// the LLM and respond "once" immediately.
//
// Only **safe** verdicts are cached. Unsafe verdicts always re-run
// through the judge so the user gets fresh reasoning if a flagged
// command resurfaces (and so a one-off "unsafe" classification can't
// permanently block a benign command).
//
// Per-session scope: the same command in a different session goes
// through the judge again. This keeps the cache narrow and avoids
// surprising cross-session approvals.
//
// In-memory, process lifetime — cleared on restart. The persisted
// ApprovedPermission DB rows cover audit and notice replay; this
// cache is purely a performance optimisation.

// commandHash returns the md5 hex of metadata["command"] when present
// and non-empty, or "" otherwise. Empty means "not cacheable" — callers
// must not record or look up against an empty hash.
//
// Only Bash permission requests carry a "command" key; all other tools
// (Edit/Write/Webfetch/…) return "" and fall through to the judge on
// every request. This matches the design constraint that the cache is
// keyed on the *exact* command string, which only makes sense for
// shell commands.
func commandHash(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["command"]
	if !ok {
		return ""
	}
	cmd, ok := raw.(string)
	if !ok || cmd == "" {
		return ""
	}
	sum := md5.Sum([]byte(cmd))
	return hex.EncodeToString(sum[:])
}

// lookupSafeCommandVerdict returns the cached safe-verdict reasoning
// for (sessionID, hash) and ok=true if an entry exists. Returns
// ("", false) on miss, nil receiver, or empty hash.
func (s *Server) lookupSafeCommandVerdict(sessionID, hash string) (string, bool) {
	if s == nil || hash == "" {
		return "", false
	}
	s.safeCommandCacheMu.Lock()
	defer s.safeCommandCacheMu.Unlock()
	bySession, ok := s.safeCommandCache[sessionID]
	if !ok {
		return "", false
	}
	reasoning, ok := bySession[hash]
	return reasoning, ok
}

// recordSafeCommandVerdict stores reasoning for (sessionID, hash) in
// the cache. No-op on nil receiver or empty hash. Overwrites any
// existing entry — the latest verdict wins.
func (s *Server) recordSafeCommandVerdict(sessionID, hash, reasoning string) {
	if s == nil || hash == "" {
		return
	}
	s.safeCommandCacheMu.Lock()
	defer s.safeCommandCacheMu.Unlock()
	if s.safeCommandCache == nil {
		s.safeCommandCache = make(map[string]map[string]string)
	}
	bySession, ok := s.safeCommandCache[sessionID]
	if !ok {
		bySession = make(map[string]string)
		s.safeCommandCache[sessionID] = bySession
	}
	bySession[hash] = reasoning
}

// emitSessionSseEvent writes an SSE event to the currently-registered
// writer for sessionID. If no client is connected, the call is a no-op.
//
// The sink is resolved on every call (not captured at goroutine start)
// so a long-running judge whose client has disconnected mid-flight
// silently drops follow-up events. The sink itself has a closed flag
// guarded by its own mutex, so even a write that races with
// unregisterSseSink cannot dereference a recycled http.ResponseWriter.
func (s *Server) emitSessionSseEvent(sessionID, eventType string, payload []byte) {
	s.lookupSseSink(sessionID).write(eventType, payload)
}

// emitPermissionPending writes the ocman.permission.pending SSE event
// to the registered writer for sessionID, if one exists. The event
// anchors the frontend countdown to an absolute wall-clock time so the
// remaining seconds are correct even if the client reconnects.
//
// judgeStartsAt is the Unix-ms moment the judge will start running
// (i.e. the moment the configured delay elapses). Passed in by callers
// so the same anchor used during the initial emit can be re-emitted on
// a replay — letting the frontend resume the countdown rather than
// restarting from zero.
//
// No-op when no client is listening; the judge runs anyway.
func (s *Server) emitPermissionPending(sessionID, permissionID string, judgeStartsAt int64) {
	// sessionID matches OpenCode's wire-format casing so the frontend
	// reducer's eventSessionId() correctly routes this event to the
	// reducer for `sessionID`. Without this, ocman events would be
	// applied to whichever session reducer is currently rendered.
	payload, err := json.Marshal(map[string]interface{}{
		"permissionId":  permissionID,
		"sessionID":     sessionID,
		"judgeStartsAt": judgeStartsAt,
	})
	if err != nil {
		return
	}
	log.WithFields(log.Fields{
		"permissionID":  permissionID,
		"sessionID":     sessionID,
		"judgeStartsAt": judgeStartsAt,
	}).Info("emitting ocman.permission.pending")
	s.emitSessionSseEvent(sessionID, "ocman.permission.pending", payload)
}

// replayAutoApproveState emits the most recent applicable
// ocman.permission.* event for an already-known permission to the
// currently-registered SSE sink.
//
// Why this exists: the headless autoApproveWatcher subscribes to
// OpenCode's /event stream from server startup, so it routinely
// observes (and claims, judges, or completes) permission.asked events
// before any frontend tab is open. When the user later opens the
// session, the REST resurrection path calls ensureAutoApprove again —
// which short-circuits because the work is already done. Without this
// replay, the just-registered SSE sink would never receive the
// pending / checking / flagged / auto-approved events that drive the
// countdown UI, leaving the prompt frozen.
//
// The frontend reducer is idempotent against repeat events (it dedups
// on permissionId / judgeStartsAt), so a replay during the same tab's
// lifetime is harmless.
func (s *Server) replayAutoApproveState(sessionID, permissionID, permission string, patterns []string) {
	st, ok := s.lookupAutoApproveStatus(sessionID, permissionID)
	if !ok {
		return
	}
	switch {
	case st.verdict == verdictSafe:
		// Safe verdicts: nothing to replay over SSE. OpenCode has
		// already cleared the prompt via RespondPermission, so the
		// REST /permissions list won't even include it on a fresh
		// page load. The approval notice is injected into the
		// message stream by injectApprovalNotices when the session
		// detail loads.
		return
	case st.verdict == verdictUnsafe:
		// Unsafe verdicts: the prompt stays pending for the human, so
		// the frontend needs the flagged reasoning to render the
		// "AI flagged this" annotation.
		if st.reasoning == "" {
			return
		}
		payload, err := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionID":    sessionID,
			"reasoning":    st.reasoning,
		})
		if err != nil {
			return
		}
		s.emitSessionSseEvent(sessionID, "ocman.permission.flagged", payload)
	case st.checking:
		// Judge is currently running — emit checking so the UI shows
		// a spinner instead of a frozen countdown.
		payload, err := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionID":    sessionID,
		})
		if err != nil {
			return
		}
		s.emitSessionSseEvent(sessionID, "ocman.permission.checking", payload)
	default:
		// Still in the pre-judge delay — emit pending with the original
		// judgeStartsAt anchor so the countdown resumes from the right
		// remaining time.
		s.emitPermissionPending(sessionID, permissionID, st.judgeStartsAt)
	}
}

// ensureAutoApprove is the single entry point for kicking off the
// auto-approve pipeline for a given permission. It:
//
//  1. If this permission already has state (in-flight goroutine or a
//     recorded verdict), replays the most recent applicable
//     ocman.permission.* event to the SSE sink — this brings a
//     freshly-connected frontend up to date with work the headless
//     watcher has already done — and returns without starting a
//     second goroutine.
//  2. Otherwise computes the judge start anchor, claims the slot,
//     emits ocman.permission.pending so the countdown starts
//     immediately on any connected client, and launches
//     backgroundAutoApprove in a goroutine.
//
// Safe to call from any handler; safe to call multiple times for the
// same permission. backgroundAutoApprove looks up the SSE sink on each
// emit so client disconnects mid-judge are non-fatal.
func (s *Server) ensureAutoApprove(
	platformID platforms.ID,
	adapter platforms.Platform,
	sessionID, permissionID, permission string,
	patterns []string,
	metadata map[string]any,
) {
	// Read the configured delay once so both the cache anchor and the
	// goroutine's sleep use the same value. The goroutine re-reads it
	// inside backgroundAutoApprove for cases where the setting was
	// changed between the asked event and the judge starting.
	delayMs := s.judgeDelayMs
	judgeStartsAt := time.Now().Add(time.Duration(delayMs) * time.Millisecond).UnixMilli()

	ctx, ok := s.claimAutoApproveWithStart(context.Background(), sessionID, permissionID, judgeStartsAt)
	if !ok {
		// Cache hit. Either another goroutine is already handling the
		// judge for this permission, or a verdict was recorded earlier
		// in this process. Replay the current state to the (possibly
		// just-registered) sink so the frontend's UI catches up.
		log.WithFields(log.Fields{
			"sessionID":    sessionID,
			"permissionID": permissionID,
		}).Debug("auto-approve: cache hit, replaying state to sink")
		s.replayAutoApproveState(sessionID, permissionID, permission, patterns)
		return
	}
	s.emitPermissionPending(sessionID, permissionID, judgeStartsAt)
	go func() {
		defer s.releaseAutoApprove(sessionID, permissionID)
		s.backgroundAutoApprove(
			ctx,
			platformID,
			adapter,
			sessionID,
			permissionID,
			permission,
			patterns,
			metadata,
		)
	}()
}

// --- Background (server-side) auto-approve ---

// ssePermissionTee wraps an io.Writer and tees the SSE byte stream to a
// side-channel that parses permission.asked events. When one is seen,
// onPermission is called so the server-side auto-approve pipeline can
// start (it routes through Server.ensureAutoApprove which is
// deduplicated against the REST-resurrection path).
//
// Parsing is line-based: SSE lines are terminated by '\n'. We buffer
// across Read boundaries so events split across multiple Write calls
// are still detected. The tee is intentionally lossy on parse errors —
// it always forwards every byte to the underlying writer unchanged.
type ssePermissionTee struct {
	w     io.Writer
	flush func()
	buf   []byte
	mu    sync.Mutex
	// onPermission fires when the upstream emits permission.asked.
	//
	// sessionID is the session ID *from the event payload*, not the
	// session that owns the SSE connection. OpenCode's /event stream
	// is process-wide and carries events for every active session, so
	// the tee on session A's connection will see permission.asked for
	// session B's prompts too. Routing must use the event's sessionID
	// — if we used the connection's sessionID, the approval notice
	// would land in the wrong session's thread.
	onPermission func(sessionID, permissionID, permission string, patterns []string, metadata map[string]any)
	// onPermissionReplied fires when the upstream emits
	// permission.replied — typically because the user answered the
	// prompt directly in the OpenCode TUI (or via any non-ocman
	// client). The handler should cancel any in-flight auto-approve
	// judge for that permission so we don't pay for it and don't
	// race the user's manual answer. sessionID is the event payload's
	// sessionID for the same reason as onPermission.
	onPermissionReplied func(sessionID, permissionID string)
	// onQuestionResolved fires when the upstream emits question.replied
	// or question.rejected, so cross-page prompt toasts for the
	// session's question can clear. reason is "replied" or "rejected".
	// Optional — nil means questions aren't observed on this tee.
	onQuestionResolved func(sessionID, requestID, reason string)
	// onSessionIdle fires when the upstream emits session.idle (the
	// agent finished a turn). Optional — nil means idle isn't observed.
	onSessionIdle func(sessionID string)
	// onSessionChanged fires when the upstream emits session.updated
	// (session created or mutated). Used to push new-session detection
	// instead of waiting for the next list poll. Optional — nil means
	// session changes aren't observed. NOTE: session.updated fires
	// frequently (per turn / token); the consumer is expected to
	// dedupe (e.g. only act on first-seen session IDs).
	onSessionChanged func(sessionID string)
}

func (t *ssePermissionTee) Write(p []byte) (int, error) {
	// Always forward bytes to the real writer first.
	n, err := t.w.Write(p)

	t.mu.Lock()
	t.buf = append(t.buf, p[:n]...)
	t.mu.Unlock()

	t.drain()

	return n, err
}

// drain processes all complete SSE events currently in the buffer.
// Runs inline in the caller's Write goroutine.
func (t *ssePermissionTee) drain() {
	t.mu.Lock()
	data := t.buf
	t.mu.Unlock()

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
			// OpenCode emits events on the SSE default channel (no
			// "event:" line), encoding the type inside the JSON payload
			// as `"type": "..."`. dispatchEvent handles both shapes:
			// named channels (eventType non-empty) and default channels
			// (eventType empty, falls back to the JSON's "type" field).
			if len(dataLines) > 0 {
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
// It fires onPermission for permission.asked and onPermissionReplied
// for permission.replied. Handlers must be non-blocking — typically
// they route through Server.ensureAutoApprove / cancelAutoApprove
// which do not block.
//
// eventType may be empty when OpenCode emits on the SSE default channel
// (no "event:" line); in that case we read the type from the JSON
// envelope's "type" field. Field names match the OpenCode OpenAPI
// schema (PermissionRequest / EventPermissionReplied).
func (t *ssePermissionTee) dispatchEvent(eventType, dataJSON string) {
	// Resolve the effective event type: named-channel header wins if
	// present, otherwise fall back to the JSON envelope's "type" field
	// (the default-channel shape that OpenCode uses).
	var typeOnly struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &typeOnly); err != nil {
		return
	}
	effectiveType := eventType
	if effectiveType == "" {
		effectiveType = typeOnly.Type
	}
	switch effectiveType {
	case "permission.asked":
		t.dispatchPermissionAsked(dataJSON)
	case "permission.replied":
		t.dispatchPermissionReplied(dataJSON)
	case "question.replied":
		t.dispatchQuestionResolved(dataJSON, "replied")
	case "question.rejected":
		t.dispatchQuestionResolved(dataJSON, "rejected")
	case "session.idle":
		t.dispatchSessionIdle(dataJSON)
	case "session.updated":
		t.dispatchSessionChanged(dataJSON)
	}
}

// dispatchPermissionAsked parses a permission.asked payload and fires
// onPermission. The metadata field is OpenCode's raw tool-input map
// (e.g. {"command":"rm bla"} for Bash) — without it the judge cannot
// distinguish two permission requests with the same generic label.
//
// sessionID is extracted from the payload (NOT from the SSE connection
// owner) because OpenCode's /event stream is process-wide: a single
// connection sees events for every session in that process. Using the
// connection's session ID for routing would attribute every other
// session's auto-approved notice to the connection's session.
func (t *ssePermissionTee) dispatchPermissionAsked(dataJSON string) {
	type permProps struct {
		ID         string         `json:"id"`
		SessionID  string         `json:"sessionID"`
		Permission string         `json:"permission"`
		Patterns   []string       `json:"patterns"`
		Metadata   map[string]any `json:"metadata"`
	}
	var envelope struct {
		Properties *permProps `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	var props permProps
	if envelope.Properties != nil && envelope.Properties.ID != "" {
		props = *envelope.Properties
	} else {
		// Flat payload (legacy or direct).
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return
		}
	}
	if props.ID == "" || props.Permission == "" || props.SessionID == "" {
		return
	}
	log.WithFields(log.Fields{
		"sessionID":    props.SessionID,
		"permissionID": props.ID,
		"permission":   props.Permission,
		"metadataKeys": metadataKeys(props.Metadata),
	}).Debug("ssePermissionTee: dispatching permission.asked")
	if t.onPermission != nil {
		t.onPermission(props.SessionID, props.ID, props.Permission, props.Patterns, props.Metadata)
	}
}

// dispatchPermissionReplied extracts the permission ID from a
// permission.replied event and fires onPermissionReplied. OpenCode
// uses `requestID` as the field name (it's the request ID of the
// original permission.asked). Falls back to `id` for compatibility
// with hypothetical flat-shape variants.
//
// sessionID is extracted from the payload for the same reason as in
// dispatchPermissionAsked: a single tee sees events for every session.
func (t *ssePermissionTee) dispatchPermissionReplied(dataJSON string) {
	type repliedProps struct {
		SessionID string `json:"sessionID"`
		RequestID string `json:"requestID"`
		ID        string `json:"id"`
	}
	var envelope struct {
		Properties *repliedProps `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	var props repliedProps
	if envelope.Properties != nil {
		props = *envelope.Properties
	} else {
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return
		}
	}
	permissionID := props.RequestID
	if permissionID == "" {
		permissionID = props.ID
	}
	if permissionID == "" || props.SessionID == "" {
		return
	}
	log.WithFields(log.Fields{
		"sessionID":    props.SessionID,
		"permissionID": permissionID,
	}).Debug("ssePermissionTee: dispatching permission.replied")
	if t.onPermissionReplied != nil {
		t.onPermissionReplied(props.SessionID, permissionID)
	}
}

// dispatchQuestionResolved extracts the session + request IDs from a
// question.replied / question.rejected event and fires
// onQuestionResolved. OpenCode uses `requestID`; both casings and the
// `id` fallback are accepted for robustness across shapes. sessionID is
// taken from the payload (the tee sees every session's events).
func (t *ssePermissionTee) dispatchQuestionResolved(dataJSON, reason string) {
	if t.onQuestionResolved == nil {
		return
	}
	type qProps struct {
		SessionID  string `json:"sessionID"`
		SessionID2 string `json:"sessionId"`
		RequestID  string `json:"requestID"`
		RequestID2 string `json:"requestId"`
		ID         string `json:"id"`
	}
	var envelope struct {
		Properties *qProps `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	var props qProps
	if envelope.Properties != nil {
		props = *envelope.Properties
	} else {
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return
		}
	}
	sessionID := firstNonEmpty(props.SessionID, props.SessionID2)
	requestID := firstNonEmpty(props.RequestID, props.RequestID2, props.ID)
	if sessionID == "" {
		return
	}
	t.onQuestionResolved(sessionID, requestID, reason)
}

// dispatchSessionIdle extracts the session ID from a session.idle event
// and fires onSessionIdle. Accepts both casings and both the enveloped
// and flat payload shapes.
func (t *ssePermissionTee) dispatchSessionIdle(dataJSON string) {
	if t.onSessionIdle == nil {
		return
	}
	type idleProps struct {
		SessionID  string `json:"sessionID"`
		SessionID2 string `json:"sessionId"`
	}
	var envelope struct {
		Properties *idleProps `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	var props idleProps
	if envelope.Properties != nil {
		props = *envelope.Properties
	} else {
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return
		}
	}
	sessionID := firstNonEmpty(props.SessionID, props.SessionID2)
	if sessionID == "" {
		return
	}
	t.onSessionIdle(sessionID)
}

// dispatchSessionChanged extracts the session ID from a session.updated
// event and fires onSessionChanged. OpenCode's session.updated payload
// is {type, properties:{sessionID, info}}; we accept both casings and
// the flat shape as a fallback.
func (t *ssePermissionTee) dispatchSessionChanged(dataJSON string) {
	if t.onSessionChanged == nil {
		return
	}
	type changedProps struct {
		SessionID  string `json:"sessionID"`
		SessionID2 string `json:"sessionId"`
	}
	var envelope struct {
		Properties *changedProps `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	var props changedProps
	if envelope.Properties != nil {
		props = *envelope.Properties
	} else {
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return
		}
	}
	sessionID := firstNonEmpty(props.SessionID, props.SessionID2)
	if sessionID == "" {
		return
	}
	t.onSessionChanged(sessionID)
}

// firstNonEmpty returns the first non-empty string from the arguments,
// or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// metadataKeys returns the sorted key list for log fields. Useful for
// debugging without dumping the full metadata into log lines (some
// values like file content can be large).
func metadataKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// backgroundAutoApprove is the authoritative auto-approve engine.
// It fires whenever an SSE permission.asked event is observed on an
// OpenCode /event stream — either via the frontend-driven tee in
// serveSessionEvents (active while a browser tab is open) or via the
// headless runAutoApproveWatcher (active for the lifetime of the
// ocman process). Both entry points funnel through ensureAutoApprove,
// which deduplicates so the judge runs at most once per permission.
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
// SSE events are emitted via emitSessionSseEvent, which resolves the
// currently-registered sink on every call. A client disconnect between
// the judge starting and finishing is non-fatal — follow-up events are
// silently dropped.
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
	metadata map[string]any,
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
	logger.WithFields(log.Fields{
		"enabled":            enabled,
		"autoApproveDefault": s.autoApproveDefault,
	}).Info("background auto-approve: checking enabled state")
	if !enabled {
		logger.Info("background auto-approve: disabled, skipping")
		return
	}

	// Per-session safe-command cache short-circuit. When the user has
	// previously approved the *exact same* Bash command in this
	// session, skip the LLM judge and the configured delay entirely:
	// respond "once", persist the audit row, and emit the SSE
	// notice. The "cached: " prefix on the stored reasoning makes the
	// origin visible in the UI and DB.
	//
	// commandHash returns "" for non-Bash tools (Edit/Write/Webfetch/…)
	// and for malformed metadata, so the cache is opt-in by data
	// shape — no Edit permission can ever auto-approve from this
	// cache, regardless of metadata content.
	if hash := commandHash(metadata); hash != "" {
		if cachedReason, ok := s.lookupSafeCommandVerdict(sessionID, hash); ok {
			logger.WithField("hash", hash).Info("background auto-approve: safe-command cache hit, skipping judge")
			finalReason := "cached: " + cachedReason
			s.recordJudgedWithReasoning(sessionID, permissionID, verdictSafe, finalReason)
			s.respondAndPersistSafeApproval(
				platformID, adapter,
				sessionID, permissionID, permission,
				patterns, finalReason,
				logger,
			)
			return
		}
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

	// Read the configured delay. Default to 0 on any error so the judge
	// still fires rather than blocking indefinitely.
	// The pending event was already emitted synchronously by the tee's
	// onPermission callback using the cached delay; we re-read here to
	// ensure the actual sleep matches the persisted value.
	delayMs := s.judgeDelayMs
	if s.stateDB != nil {
		if d, err := s.stateDB.GetJudgeDelayMs(); err == nil {
			delayMs = d
		}
	}

	// Wait the configured delay before starting the judge, giving the
	// human a window to respond manually. The context carries the
	// judgeTimeout deadline so we don't wait past it.
	if delayMs > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
		}
	}

	// Flip the status to "checking" so replayAutoApproveState can
	// route a freshly-connected sink to ocman.permission.checking
	// rather than a stale countdown.
	s.markAutoApproveChecking(sessionID, permissionID)

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
					Enabled: ps.Enabled,
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

	// Run the judge. The judge creates a transient OpenCode session,
	// sends the prompt, collects the verdict, then deletes the session
	// before returning (see JudgeWithCallback). We emit "checking" as
	// soon as the session is created so the UI can show a spinner
	// immediately. The sink is resolved on every emit so a client
	// disconnect during the (potentially slow) judge run can't panic
	// on a recycled writer.
	//
	// The judge session ID is intentionally NOT included in the
	// payload: the session is deleted shortly after the verdict is
	// extracted, so a "view judge session" link would 404 by the time
	// the user clicked it. The one-line reasoning surfaced on the
	// flagged/approved events is the durable signal.
	emitChecking := func(_ string) {
		// sessionID (all caps) matches OpenCode's wire convention so the
		// frontend reducer routes this event to the correct session.
		checkingPayload, err := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionID":    sessionID,
		})
		if err != nil {
			return
		}
		s.emitSessionSseEvent(sessionID, "ocman.permission.checking", checkingPayload)
	}
	result := s.judge.JudgeWithCallback(ctx, dbSession.Directory, permission, patterns, metadata, customSections, emitChecking)

	// If the user replied to the permission (via ocman API or directly
	// in the OpenCode TUI) while the judge was running, the cancel
	// fired and ctx.Err() is non-nil. Drop the verdict entirely:
	// - no recordJudged (the verdict is moot — the permission is
	//   already resolved; if OpenCode resurrects it for any reason we
	//   want a fresh judge rather than a stale cached verdict)
	// - no RespondPermission (OpenCode would reject it anyway)
	// - no auto-approved/flagged SSE event (the user already saw the
	//   prompt clear via permission.replied)
	// - no DB row (a notice attached to a manually-resolved prompt
	//   would be misleading)
	if ctx.Err() != nil {
		logger.WithField("ctxErr", ctx.Err()).Info("background auto-approve: cancelled before result could be applied")
		return
	}

	// Record the verdict (and reasoning) so a later ensureAutoApprove
	// call for the same permissionID (e.g. the user re-opens the
	// session and handleSessionPermissions resurrects it via REST)
	// short-circuits instead of paying for another judge run, and so
	// replayAutoApproveState can surface the flagged reasoning to a
	// newly-connected sink. Recorded regardless of verdict — unsafe
	// verdicts are the main reason this cache exists: safe verdicts
	// already auto-respond and the permission disappears from
	// OpenCode's pending list, but unsafe verdicts deliberately leave
	// the prompt pending for the human, so without this cache every
	// REST poll would re-judge.
	s.recordJudgedWithReasoning(sessionID, permissionID, result.Verdict, result.Reasoning)

	logger.WithFields(log.Fields{
		"verdict":        string(result.Verdict),
		"judgeSessionID": result.SessionID,
	}).Info("background auto-approve: judge returned")

	if result.Verdict != verdictSafe {
		// Notify connected clients so they can show the judge's one-line
		// reasoning on the permission prompt even when the AI flagged it
		// for human review. The judge session has already been deleted
		// (see JudgeWithCallback), so result.SessionID is always empty
		// and the payload no longer carries a link — only the reasoning.
		// We emit the event when there is something useful to show
		// (reasoning is the practical floor).
		if result.Reasoning != "" {
			flaggedPayload, err := json.Marshal(map[string]string{
				"permissionId": permissionID,
				"sessionID":    sessionID,
				"reasoning":    result.Reasoning,
			})
			if err == nil {
				s.emitSessionSseEvent(sessionID, "ocman.permission.flagged", flaggedPayload)
				// Broadcast so background sessions that the judge flagged
				// for human review surface in the bell / favicon / toast
				// immediately instead of waiting for the next notify poll.
				s.broadcastGlobalEvent("ocman.permission.flagged", flaggedPayload)
			}
		}
		return
	}

	// Populate the per-session safe-command cache so subsequent
	// permission.asked events for the same raw command (different
	// permissionID, same session) skip the judge entirely. Only
	// safe verdicts are cached — unsafe verdicts always re-judge so
	// the user gets fresh reasoning. Skipped when metadata has no
	// "command" key (non-Bash tools).
	if hash := commandHash(metadata); hash != "" {
		s.recordSafeCommandVerdict(sessionID, hash, result.Reasoning)
	}

	s.respondAndPersistSafeApproval(
		platformID, adapter,
		sessionID, permissionID, permission,
		patterns, result.Reasoning,
		logger,
	)
}

// respondAndPersistSafeApproval clears a pending permission in OpenCode
// (Reply="once"), persists an ApprovedPermission audit row, and emits
// ocman.permission.auto-approved to any connected SSE sink for the
// session.
//
// Shared between the live-verdict path (judge ran, returned safe) and
// the safe-command cache-hit path (judge skipped). Both paths produce
// identical user-visible outcomes — the only durable difference is the
// "cached: " prefix on `reasoning` for cache-hit rows.
//
// Uses a fresh context (not the caller's cancellable ctx) so a late
// user-reply race between the verdict and this call doesn't leave
// OpenCode without our response. Errors from the adapter are logged
// and swallowed — at worst the permission stays pending and the user
// answers it manually.
func (s *Server) respondAndPersistSafeApproval(
	platformID platforms.ID,
	adapter platforms.Platform,
	sessionID, permissionID, permission string,
	patterns []string,
	reasoning string,
	logger *log.Entry,
) {
	respondCtx, respondCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer respondCancel()
	if err := adapter.RespondPermission(respondCtx, platforms.RespondPermissionRequest{
		SessionID:    sessionID,
		PermissionID: permissionID,
		Reply:        "once",
	}); err != nil {
		logger.WithError(err).Warn("background auto-approve: failed to respond to permission")
		return
	}

	approvedAt := time.Now().UnixMilli()

	// Persist the approval so the UI notice survives a page refresh.
	// JudgeSessionID is intentionally written as the empty string: the
	// judge session has already been deleted by JudgeWithCallback (or
	// never existed for cache-hit approvals). The column is retained
	// for backwards-compat with rows written before the cleanup
	// change so existing notices keep rendering, but new rows leave
	// it empty.
	if s.stateDB != nil {
		if err := s.stateDB.RecordApprovedPermission(
			string(platformID),
			sessionID,
			state.ApprovedPermission{
				PermissionID:   permissionID,
				PermissionText: permission,
				Patterns:       patterns,
				JudgeSessionID: "",
				Reasoning:      reasoning,
				ApprovedAt:     approvedAt,
			},
		); err != nil {
			logger.WithError(err).Warn("background auto-approve: failed to persist approval")
		}
	}

	// Notify connected clients so they can inject the notice immediately
	// without waiting for a page reload. No judgeSessionId in the
	// payload — the session no longer exists; the frontend reducer
	// already falls back to permissionId for the stable notice key.
	if patterns == nil {
		patterns = []string{}
	}
	approvedPayload, err := json.Marshal(map[string]interface{}{
		"permissionId": permissionID,
		"sessionID":    sessionID,
		"permission":   permission,
		"patterns":     patterns,
		"reasoning":    reasoning,
		"approvedAt":   approvedAt,
	})
	if err == nil {
		s.emitSessionSseEvent(sessionID, "ocman.permission.auto-approved", approvedPayload)
	}

	// Broadcast the resolution to *every* connected client (not just the
	// per-session SSE sink) so cross-page prompt toasts for this session
	// can clear immediately instead of lingering until the next
	// /api/sessions/notify poll.
	s.broadcastPermissionResolved(sessionID, permissionID, "auto-approved")

	logger.Info("background auto-approve: permission approved")
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
