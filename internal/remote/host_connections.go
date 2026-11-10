package remote

import (
	"context"
	"errors"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
)

var _ hostsvc.ConnectionHost = (*remoteHost)(nil)

type connectionOperation struct {
	Action   string
	Check    hostsvc.ConnectionCheckRequest
	Setup    hostsvc.ConnectionSetupRequest
	Update   hostsvc.ConnectionUpdate
	ID, Name string
	Enabled  bool
}

type connectionCheckResult struct {
	Check  hostsvc.ConnectionCheck
	Failed bool
}

func (h *remoteHost) InventoryPlatformConnections(ctx context.Context) (hostsvc.ConnectionInventory, error) {
	return hostCall[hostsvc.ConnectionInventory](ctx, h.conn, "PlatformConnections", connectionOperation{Action: "list"})
}
func (h *remoteHost) CheckPlatformConnection(ctx context.Context, req hostsvc.ConnectionCheckRequest) (hostsvc.ConnectionCheck, error) {
	result, err := hostCall[connectionCheckResult](ctx, h.conn, "PlatformConnections", connectionOperation{Action: "check", Check: req})
	if err == nil && result.Failed {
		err = errors.New("owner setup check failed")
	}
	return result.Check, err
}
func (h *remoteHost) PreparePlatformConnection(ctx context.Context, req hostsvc.ConnectionSetupRequest) (platformconnections.Reservation, error) {
	return hostCall[platformconnections.Reservation](ctx, h.conn, "PlatformConnections", connectionOperation{Action: "prepare", Setup: req})
}
func (h *remoteHost) SavePlatformConnection(ctx context.Context, id, name string, enabled bool) (platformconnections.Connection, error) {
	return hostCall[platformconnections.Connection](ctx, h.conn, "PlatformConnections", connectionOperation{Action: "save", ID: id, Name: name, Enabled: enabled})
}
func (h *remoteHost) CancelPlatformConnectionSetup(ctx context.Context, id string) error {
	return hostExec(ctx, h.conn, "PlatformConnections", connectionOperation{Action: "cancel", ID: id})
}
func (h *remoteHost) UpdatePlatformConnection(ctx context.Context, req hostsvc.ConnectionUpdate) error {
	return hostExec(ctx, h.conn, "PlatformConnections", connectionOperation{Action: "update", Update: req})
}
func (h *remoteHost) RemovePlatformConnection(ctx context.Context, id string) error {
	return hostExec(ctx, h.conn, "PlatformConnections", connectionOperation{Action: "remove", ID: id})
}
func (h *remoteHost) CompletePlatformConnectionOnboarding(ctx context.Context) error {
	return hostExec(ctx, h.conn, "PlatformConnections", connectionOperation{Action: "onboarding"})
}
