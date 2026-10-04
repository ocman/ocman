package queuesvc

import "errors"

// ErrDeferred leaves the head held until a later idle edge, without
// consuming its failure budget. Used when older platform-held inputs
// must drain before this message can be sent.
var ErrDeferred = errors.New("follow-up deferred behind platform queue")
