package platforms

import "errors"

// ErrUnsupported is returned by Platform methods whose capability is not
// available for the concrete adapter. Handlers translate this into
// HTTP 501 Not Implemented (or gate the call behind Capabilities on
// the frontend so it never reaches the endpoint).
var ErrUnsupported = errors.New("platforms: capability not supported by this adapter")

// ErrNotFound is returned when a session or resource is not known to
// the adapter. Handlers translate this into HTTP 404.
var ErrNotFound = errors.New("platforms: not found")
