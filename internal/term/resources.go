package term

import (
	"context"
	"strconv"
	"strings"

	"github.com/ocman/ocman/internal/tmux"
)

// ResourcePIDs reports the dedicated browser-terminal server and its pane
// roots. No server is started when terminals are not running.
func ResourcePIDs(ctx context.Context) []int {
	out, err := tmux.Output(ctx, "-L", socketName, "list-panes", "-a", "-F", "#{pid} #{pane_pid}")
	if err != nil {
		return nil
	}
	var pids []int
	for _, raw := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(raw); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}
