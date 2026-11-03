//go:build !gui

package gui

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestHeadlessBuildRefusesGUI(t *testing.T) {
	started := false
	start := func(context.Context, net.Listener) error { started = true; return nil }
	if err := RunGUIWithStarter(t.Context(), start, "127.0.0.1:0", "state.db", 0); !errors.Is(err, ErrNoGUI) || started {
		t.Fatalf("err = %v, started = %v", err, started)
	}
}
