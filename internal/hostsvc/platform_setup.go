package hostsvc

import "context"

// PlatformSetupManager checks and installs prerequisites on the local host.
// Remote setup is not supported by the local Settings page.
type PlatformSetupManager interface {
	CheckPlatformSetup(context.Context, string) (PlatformSetup, error)
	InstallPlatformSetup(context.Context, string) (PlatformSetup, error)
}

// PlatformSetup is a platform's prerequisite status. Notes are the
// platform's setup instructions, shown before the checks.
type PlatformSetup struct {
	Ready       bool          `json:"ready"`
	Installable bool          `json:"installable"`
	Command     string        `json:"command"`
	Notes       []string      `json:"notes,omitempty"`
	Checks      []DoctorCheck `json:"checks"`
}
