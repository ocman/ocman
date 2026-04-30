// Package srvtiming provides per-request timing collection that the
// HTTP layer renders into the Server-Timing response header.
//
// Use it from anywhere reachable from an HTTP handler — middleware,
// adapter methods, helpers — to record how long a named phase took.
// The HTTP middleware (internal/server) attaches a Collector to the
// request context; everything else just calls Record / TimeIt
// without taking a dependency on the server package.
//
// When called outside an HTTP request (tests, background jobs) all
// functions are no-ops, so production code can sprinkle them freely
// without worrying about nil-checks.
package srvtiming

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the OTel instrumentation scope used for the child
// spans started by Begin. Kept under the srvtiming module path so
// back-ends can group "ocman phase" spans separately from the
// other server-side spans (which live under the ocman/server scope
// via internal/telemetry).
const tracerName = "github.com/NoUseFreak/ocman/internal/srvtiming"

// Collector accumulates named durations for a single request.
// Goroutine-safe: handlers may fan out work and have each goroutine
// record its own slice of the latency.
type Collector struct {
	mu      sync.Mutex
	entries []entry
}

type entry struct {
	name        string
	dur         time.Duration
	description string
}

type collectorKey struct{}

// NewCollector returns a fresh Collector. The HTTP middleware calls
// this once per request; user code should not normally need to.
func NewCollector() *Collector {
	return &Collector{}
}

// WithCollector returns a child context that carries c. Lookups via
// FromContext will return c.
func WithCollector(parent context.Context, c *Collector) context.Context {
	return context.WithValue(parent, collectorKey{}, c)
}

// FromContext returns the Collector attached to ctx by the HTTP
// middleware, or nil when ctx wasn't decorated. Callers should treat
// nil as "no-op" rather than an error.
func FromContext(ctx context.Context) *Collector {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(collectorKey{}).(*Collector)
	return c
}

// Record adds a single timing entry to the Collector in ctx, if
// present. No-op when called outside an HTTP request. The
// description is optional and shown as a tooltip in browser devtools.
//
// As a side-effect, Record also emits a span event onto the active
// span in ctx (if any) so every existing srvtiming.Record(...) call
// site instantly contributes to OTel traces. When tracing is
// disabled the span is a no-op and the event call is cheap.
func Record(ctx context.Context, name string, dur time.Duration, description string) {
	emitSpanEvent(ctx, name, dur, description)
	c := FromContext(ctx)
	if c == nil {
		return
	}
	c.add(name, dur, description)
}

// TimeIt times the execution of fn and records the result. Convenience
// wrapper for the common case of "time this single call". Equivalent
// to a Begin/End pair around fn().
func TimeIt(ctx context.Context, name string, fn func()) {
	p := Begin(ctx, name)
	defer p.End()
	fn()
}

// Phase is the handle returned by Begin. Call End (or EndWithDesc)
// when the work being timed has finished. Phases are zero-cost when
// no collector and no recording span are present, so wrapping
// short-lived work freely is fine.
//
// A zero Phase is valid and behaves as a no-op, which lets helpers
// return early without making the caller's `defer phase.End()` line
// nil-panic.
type Phase struct {
	ctx       context.Context
	collector *Collector
	span      trace.Span // child span; set when tracing is active
	name      string
	start     time.Time
}

// Begin marks the start of a named phase. The returned Phase carries
// both a srvtiming entry (so the Server-Timing header still works
// for browser devtools) and an OTel child span (so the phase shows
// up as a real span in Tempo with proper duration and is queryable
// via TraceQL like any other span).
//
// When tracing is disabled, the OTel side is the SDK no-op and only
// the srvtiming entry is emitted. When neither a collector nor a
// recording span is in ctx, Phase.End is itself a no-op.
//
// Typical use:
//
//	p := srvtiming.Begin(ctx, "db_get_session")
//	defer p.End()
//	row, err := d.db.QueryRowContext(p.Context(), ...)
//
// Pass `p.Context()` to downstream calls so any spans they create
// nest under this phase span.
func Begin(ctx context.Context, name string) Phase {
	p := Phase{
		ctx:       ctx,
		collector: FromContext(ctx),
		name:      name,
		start:     time.Now(),
	}
	// Only start a real span when something in ctx is recording.
	// Calling Tracer().Start unconditionally would clutter Tempo
	// with parent-less zero-duration spans for every srvtiming
	// call that happens outside an HTTP request (tests, boot, ...).
	if parent := trace.SpanFromContext(ctx); parent.IsRecording() {
		p.ctx, p.span = parent.TracerProvider().Tracer(tracerName).
			Start(ctx, "phase:"+name, trace.WithAttributes(
				attribute.String("ocman.phase", name),
			))
	}
	return p
}

// Context returns the context that should be used for downstream
// work inside the phase. When a child span was started this carries
// it, so spans created via the returned context nest correctly.
// Falls back to the original parent context when tracing is off.
func (p Phase) Context() context.Context {
	if p.ctx == nil {
		return context.Background()
	}
	return p.ctx
}

// End closes the phase. Records the elapsed time on the
// Server-Timing collector (if any) and ends the child span (if
// any). Safe to call on the zero Phase value.
func (p Phase) End() {
	p.endWith("")
}

// EndWithDesc is End plus a Server-Timing description. The
// description appears as the `desc=...` attribute in browser
// devtools and as `ocman.phase.description` on the OTel span.
func (p Phase) EndWithDesc(desc string) {
	p.endWith(desc)
}

func (p Phase) endWith(desc string) {
	if p.name == "" {
		return
	}
	dur := time.Since(p.start)
	if p.collector != nil {
		p.collector.add(p.name, dur, desc)
	}
	if p.span != nil {
		if desc != "" {
			p.span.SetAttributes(attribute.String("ocman.phase.description", desc))
		}
		p.span.SetAttributes(
			attribute.Float64("ocman.phase.duration_ms", float64(dur.Microseconds())/1000.0),
		)
		p.span.End()
	}
}

// emitSpanEvent decorates the active span in ctx with a timed event
// for the named phase. Cheap when no span is recording (the no-op
// span swallows the call without allocating).
func emitSpanEvent(ctx context.Context, name string, dur time.Duration, description string) {
	if ctx == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("ocman.phase", name),
		attribute.Float64("ocman.phase.duration_ms", float64(dur.Microseconds())/1000.0),
	}
	if description != "" {
		attrs = append(attrs, attribute.String("ocman.phase.description", description))
	}
	span.AddEvent("phase:"+name, trace.WithAttributes(attrs...))
}

func (c *Collector) add(name string, dur time.Duration, description string) {
	c.mu.Lock()
	c.entries = append(c.entries, entry{name: name, dur: dur, description: description})
	c.mu.Unlock()
}

// Header renders the Server-Timing header value. Returns the empty
// string when no entries have been recorded so callers can skip the
// header entirely.
//
// Format (RFC-flavoured): metric;dur=NN.N;desc="optional", ...
//
// Names are normalised: characters disallowed by the Server-Timing
// grammar (which inherits HTTP's token rules) are replaced with '_'.
// We don't reject sloppy callers — sanitisation keeps timing data
// from being silently dropped.
func (c *Collector) Header() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range c.entries {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(sanitiseName(e.name))
		fmt.Fprintf(&b, ";dur=%.1f", float64(e.dur.Microseconds())/1000.0)
		if e.description != "" {
			b.WriteString(`;desc="`)
			for _, r := range e.description {
				switch r {
				case '"', '\\':
					b.WriteByte('\\')
					b.WriteRune(r)
				default:
					b.WriteRune(r)
				}
			}
			b.WriteByte('"')
		}
	}
	return b.String()
}

// sanitiseName strips characters disallowed by the Server-Timing
// grammar. The metric name MUST match this regex:
// ^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$
// We don't try to be clever — anything outside that set becomes '_'.
// Empty inputs become "anon" so the header stays parseable.
func sanitiseName(s string) string {
	if s == "" {
		return "anon"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9',
			c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c == '!', c == '#', c == '$', c == '%', c == '&',
			c == '\'', c == '*', c == '+', c == '-', c == '.',
			c == '^', c == '_', c == '`', c == '|', c == '~':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
