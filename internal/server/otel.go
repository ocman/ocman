package server

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// withOTel wraps the mux with otelhttp's server instrumentation. It is
// intentionally an inner-most layer (closer to the mux than
// withRequestTiming) so:
//
//   - the OTel root span captures the request boundaries the same
//     way Server-Timing does, and
//   - the timing middleware sees the otelhttp-decorated handler so
//     trace context and Server-Timing stay in sync.
//
// otelhttp records http.server.request.duration / .body.size /
// .active_requests as standard semantic-convention metrics. With the
// global MeterProvider installed by telemetry.Init that's the bulk
// of the HTTP metrics surface for free.
//
// SSE is filtered out here (otelSpanFilter returns false for the
// /events suffix) so the long-lived connection doesn't keep an
// otelhttp span open for hours; the SSE handler manages its own
// connection-lifetime span instead.
func withOTel(next http.Handler) http.Handler {
	return otelhttp.NewHandler(
		next,
		"ocman.http",
		otelhttp.WithFilter(otelSpanFilter),
		otelhttp.WithSpanNameFormatter(otelSpanName),
	)
}

// otelSpanFilter returns false for paths that should NOT get an
// otelhttp-managed span. We skip:
//
//   - non-/api/ paths (static assets and SPA fallback): high volume,
//     low value. The frontend already gets browser-side spans if
//     anyone wires them up.
//   - SSE event streams: handled separately with a manually-managed
//     span at the handler boundary so we control its lifetime.
func otelSpanFilter(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	if strings.HasPrefix(path, "/api/session/") && strings.HasSuffix(path, "/events") {
		return false
	}
	return true
}

// otelSpanName returns a low-cardinality span name for an API request.
// Without this, otelhttp would name spans after the raw URL path,
// which inflates trace cardinality with every session UUID.
//
// Mapping:
//
//	/api/session/{id}                 -> METHOD /api/session/{id}
//	/api/session/{id}/{tail}          -> METHOD /api/session/{id}/{tail}
//	/api/session/{id}/{tail}/{rest}   -> METHOD /api/session/{id}/{tail}
//	(everything else)                 -> METHOD <path>
//
// Two reserved subpaths under /api/session/ — `archive` and `seen` —
// are NOT session IDs, so they're rendered verbatim.
func otelSpanName(_ string, r *http.Request) string {
	return r.Method + " " + routeTemplate(r.URL.Path)
}

// routeTemplate folds session UUIDs back into a placeholder. Exported
// only via the (unexported) helper above; lives here so the test file
// can reuse it without re-deriving the rules.
func routeTemplate(path string) string {
	const prefix = "/api/session/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	rest := strings.TrimPrefix(path, prefix)

	// Reserved non-session subpaths under /api/session/.
	switch rest {
	case "archive", "seen":
		return path
	}

	// Split off the session id and the next segment.
	id, tail, hasTail := strings.Cut(rest, "/")
	if id == "" {
		return path
	}
	if !hasTail {
		return prefix + "{id}"
	}
	head, _, _ := strings.Cut(tail, "/")
	if head == "" {
		return prefix + "{id}"
	}
	return prefix + "{id}/" + head
}
