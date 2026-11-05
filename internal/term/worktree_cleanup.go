package term

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/internal/tmux"
)

// CheckWorktreeIdle also catches terminals opened in a subdirectory, and
// terminals opened elsewhere whose shell has since changed into the checkout.
func CheckWorktreeIdle(ctx context.Context, path string) error {
	for _, prefix := range [][]string{nil, {"-L", "default"}, {"-L", socketName}} {
		args := append(prefix, "list-panes", "-a", "-F", "#{pane_current_path}\t#{pane_start_path}")
		out, err := tmux.Output(ctx, args...)
		if tmux.IsServerNotRunningError(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := checkPanePaths(path, string(out)); err != nil {
			return err
		}
	}
	return nil
}

func checkPanePaths(path, out string) error {
	for _, line := range strings.Split(out, "\n") {
		for _, dir := range strings.Split(line, "\t") {
			if dir == "" {
				continue
			}
			dir = filepath.Clean(dir)
			if dir == path || strings.HasPrefix(dir, path+string(filepath.Separator)) {
				return fmt.Errorf("terminal pane uses the worktree")
			}
		}
	}
	return nil
}
