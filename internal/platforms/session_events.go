package platforms

import "github.com/NoUseFreak/ocman/internal/db"

// SessionEventSink receives live lifecycle events from any adapter. The
// adapter updates its own live status before invoking these callbacks, so
// subsequent session and lifecycle reads agree with the event. ResetsAt is
// Unix milliseconds, zero when unknown. Idle means a completed turn, never
// a rate-limited turn. Platform is the full owner-qualified adapter ID.
type SessionEventSink struct {
	OnSessionStatus func(platform, sessionID string, status db.SessionStatus, resetsAt int64)
	OnSessionIdle   func(platform, sessionID string)
}

// SessionEventSource is implemented by adapters that produce lifecycle events.
// The sink is installed before starting their event stream.
type SessionEventSource interface {
	SetSessionEventSink(SessionEventSink)
}
