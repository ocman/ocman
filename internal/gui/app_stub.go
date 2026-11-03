//go:build !gui

package gui

import (
	"context"
	"errors"
	"net"
	"time"
)

// ErrNoGUI is returned by RunGUIWithStarter in builds without the gui tag.
var ErrNoGUI = errors.New("gui: this build has no desktop window; build with -tags gui (make build-desktop) or run without -gui")

// RunGUIWithStarter fails in headless builds, which do not link Wails.
func RunGUIWithStarter(context.Context, func(context.Context, net.Listener) error, string, string, time.Duration) error {
	return ErrNoGUI
}
