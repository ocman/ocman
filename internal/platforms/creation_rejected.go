package platforms

import "errors"

// ErrCreationRejected is an adapter's explicit guarantee that creation/fork
// failed before allocating a session. HTTP status alone is not that guarantee.
var ErrCreationRejected = errors.New("session creation was rejected before allocation")
