package acpexec

// SubagentNotification is the standard subagent extension used by codex-acp
// 2.5.0 on ACP v1. The generated Go SDK does not yet include this update kind.
// SessionID is the immediate parent, not necessarily the root session.
type SubagentNotification struct {
	SessionID string         `json:"sessionId"`
	Update    SubagentUpdate `json:"update"`
}

type SubagentUpdate struct {
	SessionID string         `json:"sessionId"`
	Title     *string        `json:"title,omitempty"`
	State     *SubagentState `json:"state,omitempty"`
}

type SubagentState struct {
	State      string `json:"state"`
	StopReason string `json:"stopReason,omitempty"`
}
