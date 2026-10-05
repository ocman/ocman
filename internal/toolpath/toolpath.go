package toolpath

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

// Ensure augments the process PATH with the PATH a login shell
// would provide. When ocman is started by launchd / a login item after
// a reboot, it inherits a minimal PATH (e.g. /usr/bin:/bin) that omits
// homebrew (/opt/homebrew/bin) and version-manager shims (mise/asdf),
// so exec.LookPath("tmux"/"opencode"/"git") fails even though they're
// installed. That surfaces as "tmux is unavailable" when creating a
// session.
//
// We ask the user's login shell for its PATH (the same trick the tmux
// launcher uses via `sh -lc`) and merge any missing entries into our
// own PATH. On any error PATH is left untouched and the error returned
// so the doctor can explain why tools look missing.
func Ensure() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	shellPath, err := loginShellPath()
	if err != nil {
		return fmt.Errorf("read login shell PATH: %w", err)
	}
	if shellPath == "" {
		return errors.New("login shell reported an empty PATH")
	}
	merged := mergePath(os.Getenv("PATH"), shellPath)
	if merged != os.Getenv("PATH") {
		_ = os.Setenv("PATH", merged)
		log.WithField("path", merged).Debug("augmented PATH from login shell")
	}
	return nil
}

// shellTimeout bounds the login shell. A healthy interactive zsh takes
// well under a second, but under load (an air rebuild, a cold mise
// cache) it overran the old 3s limit; 10s caps the startup cost of a
// genuinely hung rc file.
var shellTimeout = 10 * time.Second

// loginShellPath runs the user's login shell as an interactive login
// shell and captures the PATH it produces.
func loginShellPath() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	out, err := loginShellCmd(ctx, shell).Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s timed out after %s", shell, shellTimeout)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// loginShellCmd builds the command that asks the login shell for its
// PATH. It detaches stdin and runs the shell in its own process group so
// an interactive shell (-i) can never grab ocman's controlling
// terminal's foreground process group via tcsetpgrp and leave ocman
// backgrounded — which swallowed the user's Ctrl+C.
func loginShellCmd(ctx context.Context, shell string) *exec.Cmd {
	// -lic: login + interactive so rc files (.zshrc / .bash_profile)
	// that set up mise/asdf/homebrew run. Print PATH on its own line.
	cmd := exec.CommandContext(ctx, shell, "-lic", "command -p printf '%s' \"$PATH\"")
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// mergePath appends entries from extra that are not already present in
// base, preserving base's ordering first (so the operator's explicit
// PATH still wins for lookups).
func mergePath(base, extra string) string {
	const sep = string(os.PathListSeparator)
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(base, sep) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range strings.Split(extra, sep) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, sep)
}
