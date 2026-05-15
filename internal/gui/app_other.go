//go:build !darwin && !linux

package gui

import "github.com/wailsapp/wails/v2/pkg/options"

// platformOptions is a no-op on platforms other than darwin and linux.
func platformOptions(_ *options.App) {}
