package autoapprove

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Tee wraps an io.Writer and tees the SSE byte stream to a
// side-channel that parses permission.asked events. When one is seen,
// onPermission is called so the server-side auto-approve pipeline can
// start (it routes through Server.Ensure which is
// deduplicated against the REST-resurrection path).
//
// Parsing is line-based: SSE lines are terminated by '\n'. We buffer
// across Read boundaries so events split across multiple Write calls
// are still detected. The tee is intentionally lossy on parse errors —
// it always forwards every byte to the underlying writer unchanged.
type Tee struct {
	W     io.Writer
	Flush func()
	buf   []byte
	mu    sync.Mutex
	// onPermission fires when the upstream emits permission.asked.
	//
	// sessionID is the session ID *from the event payload*, not the
	// session that owns the SSE connection. OpenCode's directory-scoped
	// /event stream carries events for every session in that directory, so
	// the tee on session A's connection will see permission.asked for
	// session B's prompts too. Routing must use the event's sessionID
	// — if we used the connection's sessionID, the approval notice
	// would land in the wrong session's thread.
	OnPermission func(sessionID, permissionID, permission string, patterns []string, metadata map[string]any)
	// onPermissionReplied fires when the upstream emits
	// permission.replied — typically because the user answered the
	// prompt directly in the OpenCode TUI (or via any non-ocman
	// client). The handler should cancel any in-flight auto-approve
	// judge for that permission so we don't pay for it and don't
	// race the user's manual answer. sessionID is the event payload's
	// sessionID for the same reason as onPermission. reply is the
	// user's choice ("always" | "once" | "reject") so the handler can
	// capture "Allow always" approvals into the parent's shadow
	// allowlist (issue #101).
	OnPermissionReplied func(sessionID, permissionID, reply string)
	// OnPromptAsked and OnPromptResolved expose all prompt lifecycle
	// events to the adapter's live registry. Directory is populated by
	// /global/event and empty for direct /event streams.
	OnPromptAsked    func(directory, kind string, prompt platforms.LivePrompt)
	OnPromptResolved func(directory, kind, sessionID, requestID string)
	// onQuestionResolved fires when the upstream emits question.replied
	// or question.rejected, so cross-page prompt toasts for the
	// session's question can clear. reason is "replied" or "rejected".
	// Optional — nil means questions aren't observed on this tee.
	OnQuestionResolved func(sessionID, requestID, reason string)
	// onSessionIdle fires when the upstream emits session.idle (the
	// agent finished a turn). Optional — nil means idle isn't observed.
	OnSessionIdle func(sessionID string)
	// OnSessionStatus fires when the upstream emits session.status, the
	// agent's own turn-lifecycle signal. statusType is OpenCode's
	// SessionStatus discriminator: "busy", "retry" (provider backoff
	// within a turn) or "idle". This is the authoritative busy/not-busy
	// answer; ocman's message-shape inference only decides which
	// terminal state a settled session is in. Optional — nil means turn
	// state isn't observed on this tee.
	OnSessionStatus func(sessionID, statusType string)
	// onSessionChanged fires when the upstream emits session.updated
	// (session created or mutated). Used to push new-session detection
	// instead of waiting for the next list poll. Optional — nil means
	// session changes aren't observed. NOTE: session.updated fires
	// frequently (per turn / token); the consumer is expected to
	// dedupe (e.g. only act on first-seen session IDs).
	OnSessionChanged func(sessionID string)
	// OnSessionDataChanged fires for upstream events that mutate the
	// rows the session list aggregates over — message and part
	// create/update/delta/remove — and for session.deleted.
	//
	// sessionID is the affected session, or "" when the payload does
	// not identify one. An empty ID means "something changed, we don't
	// know where": consumers must treat the whole list as stale rather
	// than attribute the change to a guess, because a wrong attribution
	// puts wrong numbers in front of the user while a full
	// reconciliation only costs time. Optional.
	OnSessionDataChanged func(sessionID string)
}

func (t *Tee) Write(p []byte) (int, error) {
	// Always forward bytes to the real writer first.
	n, err := t.W.Write(p)

	t.mu.Lock()
	t.buf = append(t.buf, p[:n]...)
	t.mu.Unlock()

	t.drain()

	return n, err
}

// drain processes all complete SSE events currently in the buffer.
// Runs inline in the caller's Write goroutine.
func (t *Tee) drain() {
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
// they route through Server.Ensure / Cancel
// which do not block.
//
// eventType may be empty when OpenCode emits on the SSE default channel
// (no "event:" line); in that case we read the type from the JSON
// envelope's "type" field. Field names match the OpenCode OpenAPI
// schema (PermissionRequest / EventPermissionReplied).
func (t *Tee) dispatchEvent(eventType, dataJSON string) {
	t.dispatchEventInDirectory(eventType, dataJSON, "")
}

func (t *Tee) dispatchEventInDirectory(eventType, dataJSON, directory string) {
	// /global/event wraps the regular event envelope with its directory.
	// Feed the payload back through the same dispatcher so direct /event
	// streams and legacy flat payloads keep their existing behavior.
	var global struct {
		Directory string          `json:"directory"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &global); err == nil && len(global.Payload) > 0 {
		t.dispatchEventInDirectory("", string(global.Payload), global.Directory)
		return
	}

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
	case "permission.asked", "permission.v2.asked":
		t.dispatchPermissionAsked(directory, dataJSON)
	case "permission.replied", "permission.v2.replied":
		t.dispatchPermissionReplied(directory, dataJSON, "")
	case "permission.rejected":
		t.dispatchPermissionReplied(directory, dataJSON, "reject")
	case "question.asked":
		t.dispatchQuestionAsked(directory, dataJSON)
	case "question.replied":
		t.dispatchQuestionResolved(directory, dataJSON, "replied")
	case "question.rejected":
		t.dispatchQuestionResolved(directory, dataJSON, "rejected")
	case "session.idle":
		t.dispatchSessionIdle(dataJSON)
	case "session.status":
		t.dispatchSessionStatus(dataJSON)
	case "session.updated":
		t.dispatchSessionChanged(dataJSON)
	case "message.updated", "message.removed",
		"message.part.updated", "message.part.removed", "message.part.delta":
		// These change the messages/parts the session list aggregates
		// over (message count, tokens, cost, last role/finish/error,
		// the synthesized-terminal flag) without necessarily emitting a
		// session.updated of their own.
		t.dispatchSessionDataChanged(messageEventSessionID(dataJSON))
	case "session.deleted":
		t.dispatchSessionDataChanged(deletedSessionID(dataJSON))
	}
}

// dispatchPermissionAsked parses a permission.asked payload and fires
// onPermission. The metadata field is OpenCode's raw tool-input map
// (e.g. {"command":"rm bla"} for Bash) — without it the judge cannot
// distinguish two permission requests with the same generic label.
//
// sessionID is extracted from the payload (NOT from the SSE connection
// owner) because OpenCode's /event stream carries every session in the
// selected directory. Using the
// connection's session ID for routing would attribute every other
// session's auto-approved notice to the connection's session.
func (t *Tee) dispatchPermissionAsked(directory, dataJSON string) {
	type permProps struct {
		ID         string         `json:"id"`
		SessionID  string         `json:"sessionID"`
		Permission string         `json:"permission"`
		Patterns   []string       `json:"patterns"`
		Action     string         `json:"action"`
		Resources  []string       `json:"resources"`
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
	if props.Permission == "" {
		props.Permission = props.Action
	}
	if len(props.Patterns) == 0 {
		props.Patterns = props.Resources
	}
	if props.ID == "" || props.Permission == "" || props.SessionID == "" {
		return
	}
	log.WithFields(log.Fields{
		"sessionID":    props.SessionID,
		"permissionID": props.ID,
		"permission":   props.Permission,
		"metadataKeys": metadataKeys(props.Metadata),
	}).Debug("Tee: dispatching permission.asked")
	if t.OnPermission != nil {
		t.OnPermission(props.SessionID, props.ID, props.Permission, props.Patterns, props.Metadata)
	}
	if t.OnPromptAsked != nil {
		prompt := eventProperties(dataJSON)
		prompt["permission"] = props.Permission
		prompt["patterns"] = props.Patterns
		t.OnPromptAsked(directory, "permission", prompt)
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
func (t *Tee) dispatchPermissionReplied(directory, dataJSON, fallbackReply string) {
	type repliedProps struct {
		SessionID string `json:"sessionID"`
		RequestID string `json:"requestID"`
		ID        string `json:"id"`
		Reply     string `json:"reply"`
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
	if props.Reply == "" {
		props.Reply = fallbackReply
	}
	log.WithFields(log.Fields{
		"sessionID":    props.SessionID,
		"permissionID": permissionID,
		"reply":        props.Reply,
	}).Debug("Tee: dispatching permission.replied")
	if t.OnPermissionReplied != nil {
		t.OnPermissionReplied(props.SessionID, permissionID, props.Reply)
	}
	if t.OnPromptResolved != nil {
		t.OnPromptResolved(directory, "permission", props.SessionID, permissionID)
	}
}

func (t *Tee) dispatchQuestionAsked(directory, dataJSON string) {
	prompt := eventProperties(dataJSON)
	if promptString(prompt, "sessionID") == "" || promptString(prompt, "id") == "" {
		return
	}
	if t.OnPromptAsked != nil {
		t.OnPromptAsked(directory, "question", prompt)
	}
}

// dispatchQuestionResolved extracts the session + request IDs from a
// question.replied / question.rejected event and fires
// onQuestionResolved. OpenCode uses `requestID`; both casings and the
// `id` fallback are accepted for robustness across shapes. sessionID is
// taken from the payload (the tee sees every session's events).
func (t *Tee) dispatchQuestionResolved(directory, dataJSON, reason string) {
	if t.OnQuestionResolved == nil && t.OnPromptResolved == nil {
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
	if t.OnQuestionResolved != nil {
		t.OnQuestionResolved(sessionID, requestID, reason)
	}
	if requestID != "" && t.OnPromptResolved != nil {
		t.OnPromptResolved(directory, "question", sessionID, requestID)
	}
}

func eventProperties(dataJSON string) platforms.LivePrompt {
	var envelope struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return nil
	}
	raw := []byte(dataJSON)
	if len(envelope.Properties) > 0 {
		raw = envelope.Properties
	}
	var prompt platforms.LivePrompt
	if err := json.Unmarshal(raw, &prompt); err != nil {
		return nil
	}
	return prompt
}

func promptString(prompt platforms.LivePrompt, key string) string {
	value, _ := prompt[key].(string)
	return value
}

// dispatchSessionIdle extracts the session ID from a session.idle event
// and fires onSessionIdle. Accepts both casings and both the enveloped
// and flat payload shapes.
func (t *Tee) dispatchSessionIdle(dataJSON string) {
	if t.OnSessionIdle == nil {
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
	t.OnSessionIdle(sessionID)
}

// dispatchSessionStatus extracts the session ID and turn state from a
// session.status event and fires OnSessionStatus. OpenCode's payload is
//
//	{"type":"session.status","properties":{"sessionID":"ses_…","status":{"type":"busy"}}}
//
// The status field is accepted both as that object and as a bare string, and
// the session ID in either casing, so a shape change upstream degrades to a
// dropped event rather than a panic.
func (t *Tee) dispatchSessionStatus(dataJSON string) {
	if t.OnSessionStatus == nil {
		return
	}
	type statusProps struct {
		SessionID  string          `json:"sessionID"`
		SessionID2 string          `json:"sessionId"`
		Status     json.RawMessage `json:"status"`
	}
	var envelope struct {
		Properties *statusProps `json:"properties"`
		// The v2 event shape carries the same fields under "data".
		Data *statusProps `json:"data"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	props := statusProps{}
	switch {
	case envelope.Properties != nil:
		props = *envelope.Properties
	case envelope.Data != nil:
		props = *envelope.Data
	default:
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return
		}
	}
	sessionID := firstNonEmpty(props.SessionID, props.SessionID2)
	if sessionID == "" {
		return
	}
	t.OnSessionStatus(sessionID, sessionStatusType(props.Status))
}

// sessionStatusType reads the discriminator out of an OpenCode
// SessionStatus value, accepting either {"type":"busy"} or "busy". An
// unparseable value yields "", which every consumer treats as not-running.
func sessionStatusType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var typed struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &typed); err == nil && typed.Type != "" {
		return typed.Type
	}
	var bare string
	if err := json.Unmarshal(raw, &bare); err == nil {
		return bare
	}
	return ""
}

// dispatchSessionChanged extracts the session ID from a session.updated
// event and fires onSessionChanged. OpenCode's session.updated payload
// is {type, properties:{sessionID, info}}; we accept both casings and
// the flat shape as a fallback.
func (t *Tee) dispatchSessionChanged(dataJSON string) {
	if t.OnSessionChanged == nil {
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
	t.OnSessionChanged(sessionID)
}

// sessionRefProps is every place OpenCode puts the owning session ID on
// a message/part/session event: directly on the envelope's properties,
// or on the nested record the event carries (info for messages and
// sessions, part for parts).
type sessionRefProps struct {
	SessionID  string           `json:"sessionID"`
	SessionID2 string           `json:"sessionId"`
	Info       *sessionRefChild `json:"info"`
	Part       *sessionRefChild `json:"part"`
	Message    *sessionRefChild `json:"message"`
}

type sessionRefChild struct {
	// ID is the record's own id: the session id on a session event, the
	// message/part id on a message event. Only read where it is known
	// to be a session id.
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
}

// parseSessionRef reads the enveloped shape, falling back to the flat
// one, and returns the zero value when neither parses.
func parseSessionRef(dataJSON string) sessionRefProps {
	var envelope struct {
		Properties *sessionRefProps `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err == nil && envelope.Properties != nil {
		return *envelope.Properties
	}
	var flat sessionRefProps
	if err := json.Unmarshal([]byte(dataJSON), &flat); err != nil {
		return sessionRefProps{}
	}
	return flat
}

// messageEventSessionID resolves the session a message/part event
// belongs to. It deliberately never falls back to the record's own id:
// on a message event that is the message id, and treating it as a
// session id would mark the wrong row dirty.
func messageEventSessionID(dataJSON string) string {
	props := parseSessionRef(dataJSON)
	ids := []string{props.SessionID, props.SessionID2}
	for _, child := range []*sessionRefChild{props.Info, props.Part, props.Message} {
		if child != nil {
			ids = append(ids, child.SessionID)
		}
	}
	return firstNonEmpty(ids...)
}

// deletedSessionID resolves the session a session.deleted event refers
// to. Here the nested record IS the session, so its own id counts.
func deletedSessionID(dataJSON string) string {
	props := parseSessionRef(dataJSON)
	id := firstNonEmpty(props.SessionID, props.SessionID2)
	if id == "" && props.Info != nil {
		id = firstNonEmpty(props.Info.SessionID, props.Info.ID)
	}
	return id
}

func (t *Tee) dispatchSessionDataChanged(sessionID string) {
	if t.OnSessionDataChanged != nil {
		t.OnSessionDataChanged(sessionID)
	}
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
	return slices.Sorted(maps.Keys(m))
}
