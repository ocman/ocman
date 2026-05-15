//go:build darwin

package gui

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// platformOptions injects macOS-specific Wails window settings:
//   - Hidden-inset title bar so the traffic-light buttons overlay the toolbar
//   - Native "About" panel populated with the app name and tagline
func platformOptions(opts *options.App) {
	opts.Mac = &mac.Options{
		TitleBar: mac.TitleBarHiddenInset(),
		About: &mac.AboutInfo{
			Title:   "ocman",
			Message: "Coding-agent session dashboard",
		},
	}
}
