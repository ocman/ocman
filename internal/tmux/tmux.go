// Package tmux holds ocman's tmux process-control layer: session,
// window, and client listing; name derivation/validation; and the
// opencode/worktree launchers (with Runner seams for tests). The HTTP
// handlers that call into it stay in internal/server. This file holds
// the smallest cross-cutting primitives; the bulk lives in sessions.go.
package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const commandTimeout = 2 * time.Second

func commandContext(ctx context.Context, timeout time.Duration, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return exec.CommandContext(ctx, "tmux", args...), ctx, cancel
}

func runCommand(ctx context.Context, timeout time.Duration, args ...string) error {
	cmd, cmdCtx, cancel := commandContext(ctx, timeout, args...)
	defer cancel()
	if err := cmd.Run(); err != nil && cmdCtx.Err() != nil {
		return cmdCtx.Err()
	} else {
		return err
	}
}

// Run executes a short-lived tmux command with the package deadline.
func Run(ctx context.Context, args ...string) error {
	return runCommand(ctx, commandTimeout, args...)
}

func outputCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd, cmdCtx, cancel := commandContext(ctx, commandTimeout, args...)
	defer cancel()
	out, err := cmd.Output()
	if err != nil && cmdCtx.Err() != nil {
		return nil, cmdCtx.Err()
	}
	return out, err
}

// Output executes a short-lived tmux command and returns stdout.
func Output(ctx context.Context, args ...string) ([]byte, error) {
	return outputCommand(ctx, args...)
}

func combinedOutputCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd, cmdCtx, cancel := commandContext(ctx, commandTimeout, args...)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil && cmdCtx.Err() != nil {
		return out, cmdCtx.Err()
	}
	return out, err
}

// KillTarget kills a tmux window or session by target identifier. A
// "session:window" target (containing a colon) is killed with
// kill-window; a bare session name with kill-session. The target may
// already be gone, in which case tmux exits non-zero and the error is
// returned for the caller to treat as best-effort.
func KillTarget(ctx context.Context, target string) error {
	var args []string
	if strings.Contains(target, ":") {
		args = []string{"kill-window", "-t", target}
	} else {
		args = []string{"kill-session", "-t", target}
	}
	out, err := combinedOutputCommand(ctx, args...)
	if err != nil {
		return fmt.Errorf("tmux %v: %w: %s", args, err, string(out))
	}
	return nil
}
