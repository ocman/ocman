// Package tmux holds ocman's tmux process-control layer: session,
// window, and client listing; name derivation/validation; and the
// opencode/worktree launchers (with Runner seams for tests). The HTTP
// handlers that call into it stay in internal/server. This file holds
// the smallest cross-cutting primitives; the bulk lives in sessions.go.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// KillTarget kills a tmux window or session by target identifier. A
// "session:window" target (containing a colon) is killed with
// kill-window; a bare session name with kill-session. The target may
// already be gone, in which case tmux exits non-zero and the error is
// returned for the caller to treat as best-effort.
func KillTarget(target string) error {
	var args []string
	if strings.Contains(target, ":") {
		args = []string{"kill-window", "-t", target}
	} else {
		args = []string{"kill-session", "-t", target}
	}
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %v: %w: %s", args, err, string(out))
	}
	return nil
}
