// Package gitexec centralises construction of `git` subprocesses with a
// hardened environment. Every git invocation in ocman must go through
// this package so that two safety properties hold uniformly:
//
//  1. Git context variables (GIT_DIR, GIT_INDEX_FILE, …) are stripped.
//     Without this, ocman commands run from inside a git hook (e.g.
//     pre-commit, which exports GIT_DIR / GIT_INDEX_FILE) would be
//     redirected into the wrong repository.
//
//  2. GIT_TERMINAL_PROMPT=0 and GIT_OPTIONAL_LOCKS=0 are set, so git
//     never blocks on a credential prompt and never takes optional
//     index locks while we only read repository state.
//
// Callers pass the repository selector (`-C <dir>`) themselves, matching
// git's own CLI shape; Output is a convenience for the common
// "run and return trimmed stdout" case.
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

const maxConcurrentProcesses = 8

var processSlots = make(chan struct{}, maxConcurrentProcesses)

// contextVars lists environment variables that override git's
// repository location. They are stripped from every subprocess so a
// caller running inside a git hook can't redirect our commands into the
// wrong repository.
var contextVars = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_INDEX_FILE":                   true,
	"GIT_WORK_TREE":                    true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR":                   true,
	"GIT_CEILING_DIRECTORIES":          true,
}

// CleanEnv returns os.Environ() with git context variables (GIT_DIR,
// GIT_INDEX_FILE, …) removed. It does NOT append the prompt/locks
// safeguards — use it when you need a clean base environment to extend,
// e.g. test helpers that set GIT_AUTHOR_* identities. Production code
// should prefer Command / Output, which apply the full hardening.
func CleanEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, e := range src {
		key := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			key = e[:idx]
		}
		if contextVars[key] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// env returns CleanEnv() with the terminal-prompt / optional-locks
// safeguards appended.
func env() []string {
	return append(CleanEnv(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

// Cmd is a git subprocess whose execution shares the process-wide fork cap.
type Cmd struct {
	ctx context.Context
	cmd *exec.Cmd
}

// Command constructs a `git <args...>` command with the hardened
// environment. Callers supply the repository selector (typically
// `-C <dir>`) and may invoke Output, CombinedOutput, or Run as needed.
func Command(ctx context.Context, args ...string) *Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env()
	return &Cmd{ctx: ctx, cmd: cmd}
}

func (c *Cmd) Output() ([]byte, error) {
	return withSlot(c.ctx, c.cmd.Output)
}

func (c *Cmd) CombinedOutput() ([]byte, error) {
	return withSlot(c.ctx, c.cmd.CombinedOutput)
}

func (c *Cmd) Run() error {
	_, err := withSlot(c.ctx, func() (struct{}, error) {
		return struct{}{}, c.cmd.Run()
	})
	return err
}

func withSlot[T any](ctx context.Context, run func() (T, error)) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	select {
	case processSlots <- struct{}{}:
		defer func() { <-processSlots }()
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	return run()
}

// Output runs `git -C <dir> <args...>` and returns trimmed stdout.
// stderr is discarded; failure is signalled by a non-nil error.
func Output(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := Command(ctx, full...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
