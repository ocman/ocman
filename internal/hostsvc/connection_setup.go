package hostsvc

import (
	"context"

	"github.com/ocman/ocman/internal/platformconnections"
)

// ConnectionConfiguration is supplied by the owning integration, never by a
// browser. SeparateConfiguration permits preparing a directory, not execution
// or a claim that the tool's credentials are isolated.
type ConnectionConfiguration struct {
	ExistingDirectory     string
	SeparateConfiguration bool
}

type ConnectionSetupRequest struct {
	Kind, Configuration string
	// ReservationID resumes an existing setup. Optional kind/mode must match.
	ReservationID string
}

// ConnectionSetupHost is an optional owner seam for resumable configuration
// preparation. Remote forwarding and live Settings wiring are not enabled yet.
type ConnectionSetupHost interface {
	PreparePlatformConnection(context.Context, ConnectionSetupRequest) (platformconnections.Reservation, error)
	SavePlatformConnection(context.Context, string, string, bool) (platformconnections.Connection, error)
	CancelPlatformConnectionSetup(context.Context, string) error
}
