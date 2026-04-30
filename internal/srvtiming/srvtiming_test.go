package srvtiming

import (
	"context"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

// withRecordingTracer returns a context carrying a recording span
// produced by an in-memory tracer provider. The returned exporter
// can be queried after End() to assert what spans were emitted.
func withRecordingTracer(t *testing.T) (context.Context, *tracetest.InMemoryExporter, trace.Span) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	return ctx, exp, parent
}

func TestBegin_StartsChildSpanWhenTracingActive(t *testing.T) {
	ctx, exp, parent := withRecordingTracer(t)
	c := NewCollector()
	ctx = WithCollector(ctx, c)

	p := Begin(ctx, "db_lookup")
	time.Sleep(time.Millisecond)
	p.End()
	parent.End()

	var phase, parentSpan tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		switch s.Name {
		case "phase:db_lookup":
			phase = s
		case "parent":
			parentSpan = s
		}
	}
	if phase.Name == "" {
		t.Fatalf("missing phase span; got %+v", exp.GetSpans())
	}
	if phase.Parent.SpanID() != parentSpan.SpanContext.SpanID() {
		t.Errorf("phase span should be child of parent")
	}
	if !hasAttr(phase, "ocman.phase", "db_lookup") {
		t.Errorf("missing ocman.phase attribute")
	}
	// duration_ms attribute is recorded as a float64 so the assertion
	// just verifies the key exists.
	if !hasFloatAttr(phase, "ocman.phase.duration_ms") {
		t.Errorf("missing ocman.phase.duration_ms attribute")
	}
	// Server-Timing collector still got the entry.
	if !strings.HasPrefix(c.Header(), "db_lookup;dur=") {
		t.Errorf("collector entry missing: %q", c.Header())
	}
}

func TestBegin_NoSpanWhenTracingDisabled(t *testing.T) {
	c := NewCollector()
	ctx := WithCollector(context.Background(), c)

	p := Begin(ctx, "x")
	if p.span != nil {
		t.Errorf("expected nil span when no recording parent; got %v", p.span)
	}
	p.End()
	if !strings.HasPrefix(c.Header(), "x;dur=") {
		t.Errorf("collector entry missing: %q", c.Header())
	}
}

func TestBegin_ZeroPhaseEndIsNoop(t *testing.T) {
	var p Phase
	p.End() // must not panic
	p.EndWithDesc("ignored")
}

func TestPhase_EndWithDescPropagatesToCollectorAndSpan(t *testing.T) {
	ctx, exp, parent := withRecordingTracer(t)
	c := NewCollector()
	ctx = WithCollector(ctx, c)

	p := Begin(ctx, "step")
	p.EndWithDesc("important detail")
	parent.End()

	if !strings.Contains(c.Header(), `desc="important detail"`) {
		t.Errorf("collector header missing desc: %q", c.Header())
	}
	for _, s := range exp.GetSpans() {
		if s.Name == "phase:step" {
			if !hasAttr(s, "ocman.phase.description", "important detail") {
				t.Errorf("span missing description attribute; got %+v", s.Attributes)
			}
			return
		}
	}
	t.Fatalf("phase span not found")
}

func hasAttr(s tracetest.SpanStub, key, value string) bool {
	for _, a := range s.Attributes {
		if string(a.Key) == key && a.Value.AsString() == value {
			return true
		}
	}
	return false
}

func hasFloatAttr(s tracetest.SpanStub, key string) bool {
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return true
		}
	}
	return false
}
