package autoapprove

import (
	"encoding/json"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"maps"
	"slices"

	log "github.com/sirupsen/logrus"
)

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
	t.OnSessionStatus(sessionID, parseSessionStatus(props.Status))
}

// dispatchSessionChanged extracts the session ID from a session creation or
// update event and fires onSessionChanged.
func (t *Tee) dispatchSessionChanged(dataJSON string) {
	if t.OnSessionChanged == nil && t.OnSessionTitle == nil {
		return
	}
	props := parseSessionRef(dataJSON)
	sessionID := firstNonEmpty(props.SessionID, props.SessionID2)
	if sessionID == "" && props.Info != nil {
		sessionID = firstNonEmpty(props.Info.SessionID, props.Info.ID)
	}
	if sessionID == "" {
		return
	}
	if t.OnSessionChanged != nil {
		t.OnSessionChanged(sessionID)
	}
	if t.OnSessionTitle != nil && props.Info != nil && props.Info.Title != "" {
		t.OnSessionTitle(sessionID, props.Info.Title)
	}
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
	// Title is only meaningful on a session record.
	Title string `json:"title"`
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
