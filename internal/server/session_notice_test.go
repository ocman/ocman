package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// --- parseRateLimitNotice tests ---

func TestParseRateLimitNotice_CanonicalMessage(t *testing.T) {
	msg := "this request would exceed your account's rate limit. Please try again later"
	at := int64(1700000000000)
	got, ok := parseRateLimitNotice(msg, at)
	if !ok {
		t.Fatal("expected match")
	}
	if got.Message == "" {
		t.Error("expected non-empty message")
	}
	if got.RetryAt != 0 {
		t.Errorf("expected retryAt=0 (no retry suffix), got %d", got.RetryAt)
	}
	if got.Attempt != 0 {
		t.Errorf("expected attempt=0, got %d", got.Attempt)
	}
}

func TestParseRateLimitNotice_WithRetrySuffix(t *testing.T) {
	msg := "this request would exceed your account's rate limit. Please try again later [retrying in 5m attempt 1]"
	at := int64(1700000000000)
	got, ok := parseRateLimitNotice(msg, at)
	if !ok {
		t.Fatal("expected match")
	}
	wantRetryAt := at + 5*60*1000
	if got.RetryAt != wantRetryAt {
		t.Errorf("retryAt = %d, want %d", got.RetryAt, wantRetryAt)
	}
	if got.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", got.Attempt)
	}
}

func TestParseRateLimitNotice_RetryWithSeconds(t *testing.T) {
	msg := "rate limit exceeded [retrying in 30s attempt 3]"
	at := int64(1700000000000)
	got, ok := parseRateLimitNotice(msg, at)
	if !ok {
		t.Fatal("expected match")
	}
	wantRetryAt := at + 30*1000
	if got.RetryAt != wantRetryAt {
		t.Errorf("retryAt = %d, want %d", got.RetryAt, wantRetryAt)
	}
	if got.Attempt != 3 {
		t.Errorf("attempt = %d, want 3", got.Attempt)
	}
}

func TestParseRateLimitNotice_RetryWithHours(t *testing.T) {
	msg := "rate limit [retrying in 2h attempt 1]"
	at := int64(1700000000000)
	got, ok := parseRateLimitNotice(msg, at)
	if !ok {
		t.Fatal("expected match")
	}
	wantRetryAt := at + 2*60*60*1000
	if got.RetryAt != wantRetryAt {
		t.Errorf("retryAt = %d, want %d", got.RetryAt, wantRetryAt)
	}
}

func TestParseRateLimitNotice_RetryWithoutAttempt(t *testing.T) {
	msg := "rate limit exceeded [retrying in 10m]"
	at := int64(1700000000000)
	got, ok := parseRateLimitNotice(msg, at)
	if !ok {
		t.Fatal("expected match")
	}
	wantRetryAt := at + 10*60*1000
	if got.RetryAt != wantRetryAt {
		t.Errorf("retryAt = %d, want %d", got.RetryAt, wantRetryAt)
	}
	if got.Attempt != 0 {
		t.Errorf("attempt = %d, want 0", got.Attempt)
	}
}

func TestParseRateLimitNotice_CaseInsensitive(t *testing.T) {
	msg := "This Request Would Exceed Your Account's Rate Limit."
	_, ok := parseRateLimitNotice(msg, 0)
	if !ok {
		t.Fatal("expected case-insensitive match")
	}
}

func TestParseRateLimitNotice_UnrelatedError(t *testing.T) {
	tests := []string{
		"connection refused",
		"model not found",
		"",
		"ProviderModelNotFoundError",
	}
	for _, msg := range tests {
		if _, ok := parseRateLimitNotice(msg, 0); ok {
			t.Errorf("should not match %q", msg)
		}
	}
}

func TestParseRateLimitNotice_MalformedBracketSuffix(t *testing.T) {
	msg := "rate limit exceeded [retrying in garbage attempt abc]"
	got, ok := parseRateLimitNotice(msg, 1700000000000)
	if !ok {
		t.Fatal("expected match on the rate-limit phrase even with malformed suffix")
	}
	// Malformed duration → retryAt stays 0
	if got.RetryAt != 0 {
		t.Errorf("expected retryAt=0 for malformed duration, got %d", got.RetryAt)
	}
}

// --- deriveSessionNotice tests ---

func TestDeriveSessionNotice_ErroredWithRateLimit(t *testing.T) {
	s := db.Session{
		Status:           "error",
		LastErrorMessage: "this request would exceed your account's rate limit. Please try again later [retrying in 5m attempt 1]",
		LastErrorAt:      1700000000000,
	}
	notice := deriveSessionNotice(s)
	if notice == nil {
		t.Fatal("expected notice")
	}
	if notice.Kind != "rate_limit" {
		t.Errorf("kind = %q, want rate_limit", notice.Kind)
	}
	if notice.RetryAt != 1700000000000+5*60*1000 {
		t.Errorf("retryAt = %d, want %d", notice.RetryAt, 1700000000000+5*60*1000)
	}
	if notice.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", notice.Attempt)
	}
}

func TestDeriveSessionNotice_ErroredWithRateLimitInName(t *testing.T) {
	s := db.Session{
		Status:        "error",
		LastErrorName: "RateLimitError",
		LastErrorAt:   1700000000000,
	}
	notice := deriveSessionNotice(s)
	if notice == nil {
		t.Fatal("expected notice from error name")
	}
	if notice.Kind != "rate_limit" {
		t.Errorf("kind = %q, want rate_limit", notice.Kind)
	}
}

func TestDeriveSessionNotice_NonErrorStatus(t *testing.T) {
	for _, status := range []string{"done", "waiting", "busy"} {
		s := db.Session{
			Status:           status,
			LastErrorMessage: "rate limit exceeded",
		}
		if notice := deriveSessionNotice(s); notice != nil {
			t.Errorf("status=%q should not produce a notice, got %+v", status, notice)
		}
	}
}

func TestDeriveSessionNotice_ErroredWithoutRateLimit(t *testing.T) {
	s := db.Session{
		Status:           "error",
		LastErrorMessage: "connection refused",
	}
	if notice := deriveSessionNotice(s); notice != nil {
		t.Errorf("non-rate-limit error should not produce a notice, got %+v", notice)
	}
}

// --- applySessionNotice tests ---

func TestApplySessionNotice_EnrichesSlice(t *testing.T) {
	sessions := []db.Session{
		{
			ID:               "s1",
			Status:           "error",
			LastErrorMessage: "rate limit exceeded [retrying in 2m attempt 2]",
			LastErrorAt:      1700000000000,
		},
		{
			ID:     "s2",
			Status: "done",
		},
		{
			ID:               "s3",
			Status:           "error",
			LastErrorMessage: "model not found",
		},
	}
	applySessionNotice(sessions)

	if sessions[0].Notice == nil {
		t.Fatal("s1 should have a notice")
	}
	if sessions[0].Notice.Kind != "rate_limit" {
		t.Errorf("s1 notice kind = %q", sessions[0].Notice.Kind)
	}
	if sessions[1].Notice != nil {
		t.Error("s2 (done) should not have a notice")
	}
	if sessions[2].Notice != nil {
		t.Error("s3 (non-rate-limit error) should not have a notice")
	}
}
