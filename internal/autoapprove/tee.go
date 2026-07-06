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
	// session that owns the SSE connection. OpenCode's /event stream
	// is process-wide and carries events for every active session, so
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
	// sessionID for the same reason as onPermission.
	OnPermissionReplied func(sessionID, permissionID string)
	// onQuestionResolved fires when the upstream emits question.replied
	// or question.rejected, so cross-page prompt toasts for the
	// session's question can clear. reason is "replied" or "rejected".
	// Optional — nil means questions aren't observed on this tee.
	OnQuestionResolved func(sessionID, requestID, reason string)
	// onSessionIdle fires when the upstream emits session.idle (the
	// agent finished a turn). Optional — nil means idle isn't observed.
	OnSessionIdle func(sessionID string)
	// onSessionChanged fires when the upstream emits session.updated
	// (session created or mutated). Used to push new-session detection
	// instead of waiting for the next list poll. Optional — nil means
	// session changes aren't observed. NOTE: session.updated fires
	// frequently (per turn / token); the consumer is expected to
	// dedupe (e.g. only act on first-seen session IDs).
	OnSessionChanged func(sessionID string)
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
func (t *Tee) dispatchPermissionAsked(dataJSON string) {
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
	}).Debug("Tee: dispatching permission.asked")
	if t.OnPermission != nil {
		t.OnPermission(props.SessionID, props.ID, props.Permission, props.Patterns, props.Metadata)
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
func (t *Tee) dispatchPermissionReplied(dataJSON string) {
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
	}).Debug("Tee: dispatching permission.replied")
	if t.OnPermissionReplied != nil {
		t.OnPermissionReplied(props.SessionID, permissionID)
	}
}

// dispatchQuestionResolved extracts the session + request IDs from a
// question.replied / question.rejected event and fires
// onQuestionResolved. OpenCode uses `requestID`; both casings and the
// `id` fallback are accepted for robustness across shapes. sessionID is
// taken from the payload (the tee sees every session's events).
func (t *Tee) dispatchQuestionResolved(dataJSON, reason string) {
	if t.OnQuestionResolved == nil {
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
	t.OnQuestionResolved(sessionID, requestID, reason)
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
