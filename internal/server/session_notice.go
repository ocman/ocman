package server

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
)

// rateLimitPhrases are case-insensitive substrings that identify a
// rate-limit error. Kept broad enough to survive minor wording changes
// in OpenCode while avoiding false positives on unrelated errors.
var rateLimitPhrases = []string{
	"rate limit",
	"ratelimit",
	"would exceed your account",
}

// providerOverloadPhrases identify transient provider-side capacity
// failures. Keep these tied to overload/capacity wording so permanent
// errors like invalid API keys or missing models do not become notices.
var providerOverloadPhrases = []string{
	"overloaded",
	"overload",
	"at capacity",
	"capacity exceeded",
}

// retrySuffixRe matches the "[retrying in <delay> attempt <n>]" suffix
// that OpenCode appends to rate-limit error messages. Both the delay
// and the attempt are optional captures.
var retrySuffixRe = regexp.MustCompile(
	`\[retrying in (\d+[smh])(?: attempt (\d+))?\]`,
)

// parsedRateLimit holds the normalized fields extracted from a
// rate-limit error message.
type parsedRateLimit struct {
	Message string
	RetryAt int64
	Attempt int
}

type parsedProviderOverload struct {
	Message string
	RetryAt int64
	Attempt int
}

// parseRateLimitNotice checks whether msg matches a known rate-limit
// pattern and extracts retry metadata when present. Returns (nil, false)
// for non-matching messages. Never returns an error — malformed suffixes
// degrade gracefully to retryAt=0 / attempt=0.
func parseRateLimitNotice(msg string, at int64) (*parsedRateLimit, bool) {
	lower := strings.ToLower(msg)
	matched := false
	for _, phrase := range rateLimitPhrases {
		if strings.Contains(lower, phrase) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, false
	}

	result := &parsedRateLimit{
		Message: msg,
	}

	// Try to extract retry delay and attempt from the bracket suffix.
	if m := retrySuffixRe.FindStringSubmatch(msg); m != nil {
		if delay, ok := parseDuration(m[1]); ok {
			result.RetryAt = at + delay
		}
		if len(m) > 2 && m[2] != "" {
			if n, err := strconv.Atoi(m[2]); err == nil {
				result.Attempt = n
			}
		}
		// Strip the retry suffix from the user-facing message to
		// avoid duplicate copy when the UI renders both the message
		// and a separate retry countdown.
		result.Message = strings.TrimSpace(retrySuffixRe.ReplaceAllString(msg, ""))
	}

	return result, true
}

func parseProviderOverloadNotice(msg string, at int64) (*parsedProviderOverload, bool) {
	lower := strings.ToLower(msg)
	matched := false
	for _, phrase := range providerOverloadPhrases {
		if strings.Contains(lower, phrase) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, false
	}

	result := &parsedProviderOverload{Message: msg}
	if m := retrySuffixRe.FindStringSubmatch(msg); m != nil {
		if delay, ok := parseDuration(m[1]); ok {
			result.RetryAt = at + delay
		}
		if len(m) > 2 && m[2] != "" {
			if n, err := strconv.Atoi(m[2]); err == nil {
				result.Attempt = n
			}
		}
		result.Message = strings.TrimSpace(retrySuffixRe.ReplaceAllString(msg, ""))
	}
	return result, true
}

// parseDuration converts a compact duration string like "5m", "30s",
// or "2h" into milliseconds. Returns (0, false) for unrecognized input.
func parseDuration(s string) (int64, bool) {
	if len(s) < 2 {
		return 0, false
	}
	unit := s[len(s)-1]
	n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	switch unit {
	case 's':
		return n * 1000, true
	case 'm':
		return n * 60 * 1000, true
	case 'h':
		return n * 60 * 60 * 1000, true
	default:
		return 0, false
	}
}

// deriveSessionNotice returns a SessionNotice for a session whose
// status is "error" and whose latest error matches a known transient
// pattern. Returns nil when no notice applies.
func deriveSessionNotice(s db.Session) *db.SessionNotice {
	if s.Status != "error" {
		return nil
	}

	// Try the error message first (most specific), then the error name.
	for _, text := range []string{s.LastErrorMessage, s.LastErrorName} {
		if text == "" {
			continue
		}
		parsed, ok := parseRateLimitNotice(text, s.LastErrorAt)
		if ok {
			return &db.SessionNotice{
				Kind:    "rate_limit",
				Message: parsed.Message,
				RetryAt: parsed.RetryAt,
				Attempt: parsed.Attempt,
			}
		}
		if parsed, ok := parseProviderOverloadNotice(text, s.LastErrorAt); ok {
			return &db.SessionNotice{
				Kind:    "provider_overloaded",
				Message: parsed.Message,
				RetryAt: parsed.RetryAt,
				Attempt: parsed.Attempt,
			}
		}
	}

	return nil
}

// applySessionNotice enriches a slice of sessions with normalized
// notices. Called from the session handlers after applySessionState.
func applySessionNotice(sessions []db.Session) {
	for i := range sessions {
		sessions[i].Notice = deriveSessionNotice(sessions[i])
	}
}
