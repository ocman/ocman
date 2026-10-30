package hostsvc

import "context"

// PlatformSetupManager checks and installs prerequisites on the local host.
// Remote setup is not supported by the local Settings page.
type PlatformSetupManager interface {
	CheckPlatformSetup(context.Context, string) (PlatformSetup, error)
	InstallPlatformSetup(context.Context, string) (PlatformSetup, error)
}

type PlatformSetup struct {
	Ready       bool          `json:"ready"`
	Installable bool          `json:"installable"`
	Command     string        `json:"command"`
	Checks      []DoctorCheck `json:"checks"`
}
