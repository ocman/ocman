package claudeprovider

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/platforms/claude/launch"
)

// worktreeNameArgs is the auto-approve judge's measured one-shot command:
// Haiku, no tools, no MCP servers, no saved session. The prompt goes on stdin.
var worktreeNameArgs = []string{"-p", "--model", "haiku", "--no-session-persistence", "--strict-mcp-config",
	"--tools", "", "--output-format", "text"}

// WorktreeName names a worktree without OpenCode: one short `claude -p` call
// on Haiku titles the task, slugged into a branch name. The caller keeps the
// provisional name on any error. It takes a helper slot under slots.
func WorktreeName(ctx context.Context, slots, cli, dir, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, err := HelperSlot(ctx, slots)
	if err != nil {
		return "", err
	}
	defer release()
	runes := []rune(prompt)
	if len(runes) > 2000 {
		runes = runes[:2000]
	}
	cmd := exec.CommandContext(ctx, cli, worktreeNameArgs...)
	cmd.Dir = dir
	cmd.Env = claudecli.Environment(nil, dir)
	cmd.Stdin = strings.NewReader("Reply with only a short title (3 to 6 words) for this task, nothing else.\n\nTask:\n" + string(runes))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if name := git.TitleSlug(string(out)); name != "" {
		return name, nil
	}
	return "", errors.New("claude returned no branch name")
}
