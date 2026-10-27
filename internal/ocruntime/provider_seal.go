package ocruntime

import (
	"context"
	"net/url"
)

// SealIdle commits our idle drain to replacement, fencing a concurrent
// ResumeIdleDrain. Unsupported or lost responses never authorize Stop.
func (r *BackendRuntime) SealIdle(ctx context.Context, inst *Instance, token string) (bool, error) {
	return r.drain(ctx, inst, "/seal-idle?token="+url.QueryEscape(token))
}
