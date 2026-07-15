package workflows

import (
	"testing"
	"time"
)

func TestRetentionExpiry(t *testing.T) {
	created := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	// Default (0) → 30 days.
	wantDefault := created.Add(30 * 24 * time.Hour).UnixMilli()
	if got := retentionExpiry(created, 0); got != wantDefault {
		t.Errorf("default retention = %d, want %d", got, wantDefault)
	}
	// Override → that many days.
	want7 := created.Add(7 * 24 * time.Hour).UnixMilli()
	if got := retentionExpiry(created, 7); got != want7 {
		t.Errorf("7-day retention = %d, want %d", got, want7)
	}
	// Negative → never expires (0).
	if got := retentionExpiry(created, -1); got != 0 {
		t.Errorf("negative retention = %d, want 0 (never)", got)
	}
}

func TestRedactorReplacesKnownValues(t *testing.T) {
	r := newRedactor(map[string]string{"TOKEN": "s3cr3t", "PASS": "hunter2", "EMPTY": ""})
	got := r.redact("log line with s3cr3t and hunter2 and safe text")
	if got != "log line with "+redactionMarker+" and "+redactionMarker+" and safe text" {
		t.Fatalf("redact leaked or mangled: %q", got)
	}
	// Empty secret value must not be redacted (would corrupt everything).
	if r.redact("abc") != "abc" {
		t.Fatalf("empty secret value corrupted output")
	}
}

func TestRedactorPrefersLongestValueFirst(t *testing.T) {
	// "abc" is a substring of "abcdef"; longest-first prevents a partial
	// leak of the longer secret.
	r := newRedactor(map[string]string{"A": "abc", "B": "abcdef"})
	got := r.redact("value=abcdef")
	if got != "value="+redactionMarker {
		t.Fatalf("longest-first redaction failed: %q", got)
	}
}

func TestNilRedactorIsNoop(t *testing.T) {
	var r *redactor
	if r.redact("anything") != "anything" {
		t.Fatal("nil redactor changed input")
	}
	if got := newRedactor(nil).redact("anything"); got != "anything" {
		t.Fatalf("empty redactor changed input: %q", got)
	}
}
