package platforms

import (
	"errors"
	"fmt"
)

// ErrUnsupported is returned by Platform methods whose capability is not
// available for the concrete adapter. Handlers translate this into
// HTTP 501 Not Implemented (or gate the call behind Capabilities on
// the frontend so it never reaches the endpoint).
var ErrUnsupported = errors.New("platforms: capability not supported by this adapter")

// ErrNotFound is returned when a session or resource is not known to
// the adapter. Handlers translate this into HTTP 404.
var ErrNotFound = errors.New("platforms: not found")

// ErrBusy is returned by interactive operations (currently
// SendMessage on Claude Code) when the target session is mid-turn and
// accepting another prompt would corrupt the conversation tree. See
// AD-13 and spec/multi-agent-support/phase7/findings.md. Handlers
// translate this into HTTP 409 Conflict.
var ErrBusy = errors.New("platforms: session is currently processing a prompt")

// ErrPlatformUnreachable is returned when an operation needs a live
// platform process (e.g. an OpenCode instance bound to a directory)
// but none can be discovered. This is distinct from ErrNotFound —
// the session/directory is known, but nothing is listening. Handlers
// translate this into HTTP 503 Service Unavailable so the frontend
// can offer to launch the missing process (see launchOpencodeInTmux).
var ErrPlatformUnreachable = errors.New("platforms: no running instance for this location")

// ErrUpstreamRejected is returned when a live platform process *did*
// reach us but refused the request with a 4xx response (e.g.
// OpenCode rejecting a SendMessage with `ProviderModelNotFoundError`
// because the requested model isn't configured). It is distinct from
// the default-bucket "we couldn't reach the platform" case so that
// handlers can pass the upstream-supplied human message through to
// the UI instead of replacing it with a generic banner. Handlers
// translate this into HTTP 422 Unprocessable Entity.
//
// Adapters returning this error should wrap it in an UpstreamError
// so callers can recover the message and HTTP status.
var ErrUpstreamRejected = errors.New("platforms: upstream rejected the request")

// UpstreamError carries a human-readable message captured from a
// platform's 4xx response. `errors.Is(err, ErrUpstreamRejected)` is
// true. The Message is what the user sees; Status preserves the
// upstream HTTP status for logs.
type UpstreamError struct {
	// Status is the HTTP status returned by the upstream platform
	// (e.g. 400). Informational only — the public HTTP response is
	// always 422.
	Status int
	// Message is the human-readable error to surface to the UI.
	// May be empty if the upstream response was unparseable, in
	// which case handlers should fall back to a generic message.
	Message string
}

func (e *UpstreamError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("upstream HTTP %d", e.Status)
	}
	return fmt.Sprintf("upstream HTTP %d: %s", e.Status, e.Message)
}

// Is reports that an UpstreamError matches the ErrUpstreamRejected
// sentinel so handlers can branch via errors.Is.
func (e *UpstreamError) Is(target error) bool {
	return target == ErrUpstreamRejected
}
