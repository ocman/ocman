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
	out, err := tmux.Output(ctx, "-L", socketName, "list-panes", "-a", "-F", "#{pane_current_path}\t#{pane_start_path}")
	if tmux.IsServerNotRunningError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return checkPanePaths(path, string(out))
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
