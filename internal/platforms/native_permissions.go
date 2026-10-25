package platforms

import "context"

// NativePermissionState describes the platform's own approval engine, separate
// from ocman's permission rules and auto-approval judge.
type NativePermissionState struct {
	Mode string `json:"mode"`
	// Pending means no child has accepted this mode yet; it applies on next send.
	Pending bool `json:"pending,omitempty"`
}

// NativePermissions is optional. Gate it with Capabilities.NativeAutoPermission.
type NativePermissions interface {
	NativePermissionState(context.Context, string) (*NativePermissionState, error)
}
