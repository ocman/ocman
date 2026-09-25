package term

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/tmux"
	log "github.com/sirupsen/logrus"
)

// SweepLegacySessions removes orphaned ephemeral viewer sessions
// from the previous implementation (`ocman-view-<uuid>`). They could
// never belong to a live connection at startup, so killing them at boot
// self-heals leaks across restarts. Safe no-op when tmux isn't running.
// These legacy sessions live on the default server, not the terminal server.
func SweepLegacySessions(ctx context.Context) {
	if !tmux.IsAvailable() {
		return
	}
	sweepLegacySessions(func() ([]string, error) { return listSessionNames(ctx) }, func(name string) error { return killSession(ctx, name) })
}

func listSessionNames(ctx context.Context) ([]string, error) {
	out, err := tmux.Output(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

func killSession(ctx context.Context, name string) error {
	return tmux.Run(ctx, "kill-session", "-t", name)
}

// sweepLegacySessions is the seam-injected core of SweepLegacySessions so
// tests can assert which sessions get killed without a real tmux server.
func sweepLegacySessions(list func() ([]string, error), kill func(string) error) {
	names, err := list()
	if err != nil {
		return
	}
	for _, name := range names {
		if !strings.HasPrefix(name, legacyViewPrefix) {
			continue
		}
		if err := kill(name); err != nil {
			log.WithError(err).WithField("session", name).
				Debug("sweeping legacy ocman-view session")
		}
	}
}

// legacyViewPrefix names the ephemeral viewer sessions of the previous
// implementation. Only sessions carrying it are swept.
const legacyViewPrefix = "ocman-view-"
