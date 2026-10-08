package opencode

import (
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
)

type liveSessionStatus struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Attempt int    `json:"attempt"`
	Next    int64  `json:"next"`
}

// RetryNotice preserves OpenCode's live retry metadata, which is not stored
// on the assistant message until retries are exhausted.
func RetryNotice(message string, next int64, attempt int) *db.SessionNotice {
	kind := "retry"
	lower := strings.ToLower(message)
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "ratelimit") || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "rate-limit") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "would exceed your account") {
		kind = "rate_limit"
	} else if strings.Contains(lower, "overload") || strings.Contains(lower, "at capacity") || strings.Contains(lower, "capacity exceeded") {
		kind = "provider_overloaded"
	}
	if message == "" {
		message = "Provider request is being retried."
	}
	return &db.SessionNotice{Kind: kind, Message: message, RetryAt: next, Attempt: attempt}
}

func (a *Adapter) sessionNoticeOnPort(sessionID, port string) *db.SessionNotice {
	if a == nil || a.turns == nil || port == "" {
		return nil
	}
	a.turns.mu.RLock()
	defer a.turns.mu.RUnlock()
	entry := a.turns.entries[sessionID]
	if entry.port != port || entry.notice == nil {
		return nil
	}
	notice := *entry.notice
	return &notice
}

func sameNotice(a, b *db.SessionNotice) bool {
	return a == b || (a != nil && b != nil && *a == *b)
}
