//go:build darwin

package gui

import (
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// platformOptions injects macOS-specific Wails window settings:
//   - Hidden-inset title bar so the traffic-light buttons overlay the toolbar
//   - Native "About" panel with the tagline, version and log path
func platformOptions(opts *options.App) {
	opts.Mac = &mac.Options{
		TitleBar: mac.TitleBarHiddenInset(),
		About: &mac.AboutInfo{
			Title:   "ocman",
			Message: fmt.Sprintf("Coding-agent session dashboard\n\nVersion %s\nLogs: %s", Version, fatalLogPath),
		},
	}
}
