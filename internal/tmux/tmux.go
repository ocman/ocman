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
	out, err := cmd.CombinedOutput()
	if err != nil && cmdCtx.Err() != nil {
		return cmdCtx.Err()
	}
	if err != nil {
		diagnostic := strings.TrimSpace(string(out))
		for i, arg := range args {
			if i > 0 && args[i-1] == "-e" {
				if _, value, ok := strings.Cut(arg, "="); ok && value != "" {
					diagnostic = strings.ReplaceAll(diagnostic, value, "<redacted>")
				}
			}
		}
		return fmt.Errorf("tmux command failed: %w: %s", err, diagnostic)
	}
	return nil
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
