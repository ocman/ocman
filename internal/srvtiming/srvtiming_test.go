package srvtiming

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCollector_HeaderEmpty(t *testing.T) {
	c := NewCollector()
	if got := c.Header(); got != "" {
		t.Errorf("empty collector should produce empty header, got %q", got)
	}
}

func TestCollector_HeaderSingleEntry(t *testing.T) {
	c := NewCollector()
	c.add("db", 12*time.Millisecond, "")
	got := c.Header()
	if got != "db;dur=12.0" {
		t.Errorf("got %q, want %q", got, "db;dur=12.0")
	}
}

func TestCollector_HeaderWithDescription(t *testing.T) {
	c := NewCollector()
	c.add("db", 12*time.Millisecond, "GET /foo")
	got := c.Header()
	if got != `db;dur=12.0;desc="GET /foo"` {
		t.Errorf("got %q", got)
	}
}

func TestCollector_HeaderEscapesQuotes(t *testing.T) {
	c := NewCollector()
	c.add("db", time.Millisecond, `with "quotes" and \backslash`)
	got := c.Header()
	want := `db;dur=1.0;desc="with \"quotes\" and \\backslash"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCollector_HeaderMultipleEntries(t *testing.T) {
	c := NewCollector()
	c.add("a", 1*time.Millisecond, "")
	c.add("b", 2*time.Millisecond, "")
	got := c.Header()
	if !strings.Contains(got, "a;dur=1.0") || !strings.Contains(got, "b;dur=2.0") {
		t.Errorf("missing expected entries: %q", got)
	}
	if !strings.Contains(got, ", ") {
		t.Errorf("entries should be comma-space separated: %q", got)
	}
}

func TestSanitiseName_DropsInvalidCharacters(t *testing.T) {
	cases := map[string]string{
		"":              "anon",
		"plain":         "plain",
		"db_query":      "db_query",
		"db query":      "db_query",
		"http;get":      "http_get",
		"a/b":           "a_b",
		"unicode-café":  "unicode-caf__",
		"!#$%&'*+-.^|~": "!#$%&'*+-.^|~",
	}
	for in, want := range cases {
		if got := sanitiseName(in); got != want {
			t.Errorf("sanitiseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecord_NoopWhenContextHasNoCollector(t *testing.T) {
	// Must not panic.
	Record(context.Background(), "x", time.Millisecond, "")
	TimeIt(context.Background(), "y", func() {})
}

func TestRecord_RoundTripThroughContext(t *testing.T) {
	c := NewCollector()
	ctx := WithCollector(context.Background(), c)
	Record(ctx, "phase", 5*time.Millisecond, "test")
	got := c.Header()
	if got != `phase;dur=5.0;desc="test"` {
		t.Errorf("got %q", got)
	}
}

func TestTimeIt_RecordsDuration(t *testing.T) {
	c := NewCollector()
	ctx := WithCollector(context.Background(), c)
	TimeIt(ctx, "sleep", func() {
		time.Sleep(2 * time.Millisecond)
	})
	got := c.Header()
	if !strings.HasPrefix(got, "sleep;dur=") {
		t.Fatalf("expected sleep entry, got %q", got)
	}
	// Don't assert exact duration — just that it's non-zero. The
	// time.Sleep budget is too noisy for tighter bounds across CI
	// environments.
	if strings.Contains(got, "dur=0.0") {
		t.Errorf("expected non-zero duration, got %q", got)
	}
}
