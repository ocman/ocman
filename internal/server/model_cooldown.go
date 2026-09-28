package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// quotaActionReasons are retry action reasons that mean the account is
// out of tokens, however soon OpenCode plans to retry.
var quotaActionReasons = map[string]bool{"account_rate_limit": true, "free_tier_limit": true}

// onSessionRetry cools the session's provider when a retry parks longer
// than the patience threshold or names a quota wall. A short backoff is
// OpenCode's business.
func (s *Server) onSessionRetry(sessionID string, st autoapprove.SessionStatus) {
	var wait time.Duration
	if st.Next > 0 {
		next := time.UnixMilli(st.Next)
		// ponytail: one entry per retrying session, dropped on its idle edge.
		s.retryNext.Store(sessionID, next)
		wait = time.Until(next)
	}
	ctx := context.Background()
	if patience, _ := s.cooldownTimes(ctx); !quotaActionReasons[st.ActionReason] && wait <= patience {
		return
	}
	if msg, ok := s.latestAssistant(ctx, string(opencode.PlatformID), sessionID); ok {
		s.recordCooldown(ctx, msg.ProviderID, wait)
		s.abortParkedRetry(ctx, sessionID, msg.ProviderID)
	}
}

// recordQuotaCooldown cools the provider of a turn that ended in a 429
// and returns it ("" when the turn did not die on a quota wall).
// Auth failures and context overflow are deliberately ignored: another
// model fixes neither, and switching would hide the misconfiguration.
func (s *Server) recordQuotaCooldown(ctx context.Context, platformID, sessionID string) string {
	next, _ := s.retryNext.LoadAndDelete(sessionID)
	msg, ok := s.latestAssistant(ctx, platformID, sessionID)
	if !ok || msg.Error == nil || msg.Error.Name != "APIError" || msg.Error.Data.StatusCode != http.StatusTooManyRequests {
		return ""
	}
	d := quotaResetIn(msg.Error.Data.ResponseHeaders, time.Now())
	if t, ok := next.(time.Time); ok && d <= 0 {
		d = time.Until(t)
	}
	s.recordCooldown(ctx, msg.ProviderID, d)
	return msg.ProviderID
}

func (s *Server) recordCooldown(ctx context.Context, provider string, d time.Duration) {
	if s.sessions != nil && provider != "" {
		s.sessions.RecordCooldown(ctx, provider, d)
	}
}

// assistantMessage is the slice of an OpenCode assistant message that
// quota detection reads.
type assistantMessage struct {
	Role       string `json:"role"`
	ProviderID string `json:"providerID"`
	Error      *struct {
		Name string `json:"name"`
		Data struct {
			StatusCode      int               `json:"statusCode"`
			ResponseHeaders map[string]string `json:"responseHeaders"`
		} `json:"data"`
	} `json:"error"`
}

// latestAssistant returns the session's newest message when it is an
// assistant message.
func (s *Server) latestAssistant(ctx context.Context, platformID, sessionID string) (assistantMessage, bool) {
	var msg assistantMessage
	if s.registry == nil {
		return msg, false
	}
	p, ok := s.adapterForSession(ctx, platformID, sessionID)
	if !ok {
		return msg, false
	}
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || len(detail.Messages) == 0 ||
		json.Unmarshal(detail.Messages[0].Data, &msg) != nil || msg.Role != "assistant" {
		return msg, false
	}
	return msg, true
}

// quotaResetIn reads how long until the provider accepts requests again
// from persisted response headers; <= 0 when none is known.
func quotaResetIn(headers map[string]string, now time.Time) time.Duration {
	h := make(map[string]string, len(headers))
	for k, v := range headers {
		h[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	if ms, err := strconv.ParseFloat(h["retry-after-ms"], 64); err == nil {
		return time.Duration(ms * float64(time.Millisecond))
	}
	if v := h["retry-after"]; v != "" {
		if sec, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(sec * float64(time.Second))
		}
		if t, err := http.ParseTime(v); err == nil {
			return t.Sub(now)
		}
	}
	// ponytail: the latest of the anthropic-ratelimit-*-reset headers,
	// not the one whose remaining hit zero; the patience floor absorbs
	// the difference.
	var latest time.Time
	for k, v := range h {
		if strings.HasPrefix(k, "anthropic-ratelimit-") && strings.HasSuffix(k, "-reset") {
			if t, err := time.Parse(time.RFC3339, v); err == nil && t.After(latest) {
				latest = t
			}
		}
	}
	if latest.IsZero() {
		return 0
	}
	return latest.Sub(now)
}
