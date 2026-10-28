package provider

import (
	"context"

	"github.com/ocman/ocman/internal/providerprotocol"
)

// SessionOwnership probes the existing runtime only. Unsupported supervisors
// must never be treated as evidence that a session can be taken over.
func (a *Adapter) SessionOwnership(ctx context.Context, id string) (*providerprotocol.SessionOwnership, error) {
	var result providerprotocol.SessionOwnership
	err := a.call(ctx, a.hello.Capabilities.Ownership, "GET", sessionPath(id, "/ownership"), nil, &result)
	return &result, err
}
