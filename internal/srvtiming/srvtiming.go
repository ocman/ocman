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
// wrapper for the common case of "time this single call".
func TimeIt(ctx context.Context, name string, fn func()) {
	c := FromContext(ctx)
	if c == nil {
		// Still emit the span event when there's no collector — a
		// background job may be tracing without using Server-Timing.
		start := time.Now()
		fn()
		emitSpanEvent(ctx, name, time.Since(start), "")
		return
	}
	start := time.Now()
	defer func() {
		dur := time.Since(start)
		emitSpanEvent(ctx, name, dur, "")
		c.add(name, dur, "")
	}()
	fn()
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
