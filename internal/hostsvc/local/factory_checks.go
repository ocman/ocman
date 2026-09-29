package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
)

// factoryCheckOutputTail bounds what one command contributes to the report
// sent back into the validator's context.
const factoryCheckOutputTail = 4000

// RunFactoryChecks runs Formula-declared verification commands, in order, in
// the Factory branch worktree and renders a report for the validator. The
// commands come from a Formula a human approved; they run with ocman's own
// privileges, not the agent's permission profile.
func (h *Host) RunFactoryChecks(ctx context.Context, repoRoot, branch, target string, commands []string) (bool, string, error) {
	dir, err := git.FactoryWorktreePath(ctx, repoRoot, branch)
	if err != nil {
		return false, "", err
	}
	head, dirty, err := git.FactoryWorktreeState(ctx, dir)
	if err != nil {
		return false, "", err
	}
	passed := !dirty
	var report strings.Builder
	if dirty {
		report.WriteString("The worktree had uncommitted changes before the checks ran; results are not trustworthy.\n\n")
	}
	for _, command := range commands {
		start := time.Now()
		output, code := runFactoryCheck(ctx, dir, command)
		passed = passed && code == 0
		fmt.Fprintf(&report, "$ %s\nexit %d after %s\n", command, code, time.Since(start).Round(time.Second))
		if output = strings.TrimSpace(output); output != "" {
			if len(output) > factoryCheckOutputTail {
				output = "…" + output[len(output)-factoryCheckOutputTail:]
			}
			fmt.Fprintf(&report, "```\n%s\n```\n", output)
		}
		report.WriteString("\n")
	}
	// A check that commits, or leaves files behind, invalidates "passed at HEAD"
	// and would fail the clean-worktree handoff anyway.
	if after, dirtyAfter, err := git.FactoryWorktreeState(ctx, dir); err != nil || after != head || dirtyAfter {
		passed = false
		fmt.Fprintf(&report, "The worktree changed while the checks ran (HEAD %s → %s, uncommitted changes: %v, error: %v). Ignore build output in .gitignore or fix the commands; the branch must stay clean at %s.\n\n", head, after, dirtyAfter, err, head)
	}
	flags, err := git.FactoryDiffRedFlags(ctx, dir, target)
	switch {
	case err != nil:
		fmt.Fprintf(&report, "Test-integrity scan unavailable: %v\n", err)
	case len(flags) == 0:
		report.WriteString("Test-integrity scan: no deleted tests or new skip/suppression markers.\n")
	default:
		report.WriteString("Test-integrity scan found changes that can make checks pass without the work. Confirm each is justified by the plan:\n- " + strings.Join(flags, "\n- ") + "\n")
	}
	return passed, report.String(), nil
}

func runFactoryCheck(ctx context.Context, dir, command string) (string, int) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	// CI keeps test runners out of watch/interactive modes. The commands run
	// agent-written code, so ocman's own credentials are withheld.
	cmd.Env = append(factoryCheckEnv(os.Environ()), "CI=1")
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case ctx.Err() != nil:
		return string(out) + "\n[timed out]", -1
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	default:
		return string(out) + "\n" + err.Error(), -1
	}
}

var factoryCheckSecretEnv = map[string]bool{"GITHUB_TOKEN": true, "GH_TOKEN": true, "FORGEJO_TOKEN": true, "GITEA_TOKEN": true}

func factoryCheckEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if factoryCheckSecretEnv[key] || strings.HasPrefix(key, "OCMAN_") || strings.HasPrefix(key, "OTEL_") {
			continue
		}
		out = append(out, entry)
	}
	return out
}
