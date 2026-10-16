package term

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

// A viewer links only its selected window, so shell exit destroys its
// session and detaches the client instead of showing another project's shell.
func createViewer(ctx context.Context, target string) (string, func(), error) {
	name := "ocman-view-" + rand.Text()
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tmux.Run(ctx, "-L", socketName, "kill-session", "-t", name)
	}
	err := tmux.Run(ctx, "-L", socketName,
		"new-session", "-d", "-s", name, "-n", "_ocman_placeholder", ";",
		"link-window", "-k", "-s", target, "-t", name+":_ocman_placeholder", ";",
		"set-option", "-t", name, "status", "off", ";",
		"set-option", "-t", name, "detach-on-destroy", "on")
	if err != nil {
		cleanup()
	}
	return name, cleanup, err
}
