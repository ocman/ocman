package autoapprove

import "encoding/json"

// SessionStatus is one parsed OpenCode SessionStatus value. Type is the
// discriminator ("busy", "retry", "idle"); the remaining fields are only
// set on a retry status and are zero otherwise:
//
//	{"type":"retry","attempt":3,"message":"rate limited","action":{"reason":"..."},"next":1760000000000}
type SessionStatus struct {
	Type    string
	Attempt int
	Message string
	// ActionReason is action.reason when the status carries an action.
	ActionReason string
	// Next is the epoch milliseconds of the next attempt.
	Next int64
}

// parseSessionStatus reads an OpenCode SessionStatus, accepting either the
// object form or a bare type string. An unparseable value yields the zero
// status, whose empty Type every consumer treats as not-running.
func parseSessionStatus(raw json.RawMessage) SessionStatus {
	if len(raw) == 0 {
		return SessionStatus{}
	}
	var obj struct {
		Type    string `json:"type"`
		Attempt int    `json:"attempt"`
		Message string `json:"message"`
		Action  *struct {
			Reason string `json:"reason"`
		} `json:"action"`
		Next int64 `json:"next"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Type != "" {
		s := SessionStatus{Type: obj.Type, Attempt: obj.Attempt, Message: obj.Message, Next: obj.Next}
		if obj.Action != nil {
			s.ActionReason = obj.Action.Reason
		}
		return s
	}
	var bare string
	if err := json.Unmarshal(raw, &bare); err == nil {
		return SessionStatus{Type: bare}
	}
	return SessionStatus{}
}
