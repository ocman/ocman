package analytics

import (
	"net/http"
	"path/filepath"
	"testing"
)

func TestSubscriptionLabels(t *testing.T) {
	for seconds, want := range map[int64]string{5 * 60 * 60: "5 hours", 7 * 24 * 60 * 60: "7 days", 2628000: "Monthly", 60: "Usage window"} {
		if got := usageWindowName(seconds); got != want {
			t.Errorf("usageWindowName(%d) = %q, want %q", seconds, got, want)
		}
	}
	for status, want := range map[int]string{http.StatusUnauthorized: "unauthorized", http.StatusForbidden: "unauthorized", http.StatusTooManyRequests: "rate_limited", http.StatusBadGateway: "upstream_error"} {
		if got := subscriptionHTTPStatus(status); got != want {
			t.Errorf("subscriptionHTTPStatus(%d) = %q, want %q", status, got, want)
		}
	}
	if got := unixTime(0); got != "" {
		t.Errorf("unixTime(0) = %q, want empty", got)
	}
	if got := unixTime(1700000000); got != "2023-11-14T22:13:20Z" {
		t.Errorf("unixTime = %q", got)
	}
}

func TestOpenCodeAuthPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got := openCodeAuthPath(); got != filepath.Join("/xdg", "opencode", "auth.json") {
		t.Errorf("XDG path = %q", got)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got := openCodeAuthPath(); got != filepath.Join("/home/u", ".local", "share", "opencode", "auth.json") {
		t.Errorf("home path = %q", got)
	}
}
