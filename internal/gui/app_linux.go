//go:build linux

package gui

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// platformOptions injects Linux-specific Wails window settings.
// The Linux backend uses a GTK WebKitGTK window; options are more limited
// than macOS but we can set the app ID used for the desktop entry.
func platformOptions(opts *options.App) {
	opts.Linux = &linux.Options{
		// WebkitGTK uses this for the WM_CLASS hint, which desktop
		// environments use to group windows and match .desktop entries.
		ProgramName: "ocman",
	}
}
