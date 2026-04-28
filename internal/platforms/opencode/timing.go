package opencode

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// slowOpThreshold is the wall-clock duration above which a timed
// operation logs at INFO instead of DEBUG. We chose 200ms — half the
// HTTP-request slow threshold — because the operations we wrap here
// (DB walks, upstream HTTP calls) are sub-components of a request, so
// 200ms within one of them is noteworthy even if the request as a
// whole stays under the request-level threshold.
const slowOpThreshold = 200 * time.Millisecond

// observeDuration emits one structured log entry summarising a timed
// operation. Used by timeIt() and also callable directly by code
// paths that want to time something other than a single function
// scope (e.g. a paired open/close).
//
// The level is chosen by threshold rather than by the caller so the
// "what's slow" signal is uniform across the package: scan the log
// for `level=info msg="opencode op"` and you have every internal
// hot path that took ≥ slowOpThreshold without grepping per-op
// thresholds. Sub-threshold entries land at DEBUG so they're
// invisible in default production logs but available when you turn
// debug on.
//
// `fields` is merged with the standard {op, duration_ms} entries.
// nil is allowed and treated as no extra fields.
func observeDuration(op string, dur time.Duration, fields log.Fields) {
	merged := log.Fields{
		"op":          op,
		"duration_ms": dur.Milliseconds(),
	}
	for k, v := range fields {
		merged[k] = v
	}
	if dur >= slowOpThreshold {
		log.WithFields(merged).Info("opencode op")
	} else {
		log.WithFields(merged).Debug("opencode op")
	}
}

// timeIt returns a closer that, when invoked, logs the elapsed time
// since timeIt was called. Designed for the canonical
//
//	defer timeIt("session_changes_db", log.Fields{"sessionID": id})()
//
// pattern, which is the cheapest way to instrument a function scope
// without restructuring the function. The trailing `()` is critical
// — without it the deferred value is the closure itself, not its
// result.
func timeIt(op string, fields log.Fields) func() {
	start := time.Now()
	return func() {
		observeDuration(op, time.Since(start), fields)
	}
}
