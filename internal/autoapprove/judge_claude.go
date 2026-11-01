package autoapprove

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms/claude/launch"
)

// ClaudeJudge is the judge backend used when no OpenCode instance is
// reachable (AD-8): one throwaway `claude -p` call on Haiku with no tools,
// no MCP servers and no saved session. The prompt is the OpenCode judge's.
type ClaudeJudge struct {
	CLI string // claude executable
	// Slot, when set, takes a machine-wide helper slot for the call: judges
	// are bounded but never wait behind the pooled session children.
	Slot func(context.Context) (func(), error)
}

// ClaudeJudgeArgs is the fixed command line; the prompt goes on stdin.
var ClaudeJudgeArgs = []string{"-p", "--model", "haiku", "--no-session-persistence", "--strict-mcp-config",
	"--tools", "", "--output-format", "text"}

func (j ClaudeJudge) JudgeWithCallback(ctx context.Context, _, permission string, patterns []string, metadata map[string]any, customSections []PromptSection, _ func(string)) JudgeResult {
	ctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()
	if j.Slot != nil {
		release, err := j.Slot(ctx)
		if err != nil {
			log.WithError(err).Warn("auto-approve judge: no claude helper slot")
			return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
		}
		defer release()
	}
	// Run in a fresh empty directory, never the session's project: the
	// project's CLAUDE.md, settings hooks and memory would otherwise be
	// loaded into the judge and could steer its verdict.
	neutral, err := os.MkdirTemp("", "ocman-judge-")
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: no neutral directory for claude -p")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	defer func() { _ = os.RemoveAll(neutral) }()
	cmd := exec.CommandContext(ctx, j.CLI, ClaudeJudgeArgs...)
	cmd.Dir = neutral
	// A child that exits but leaves stdout open (a backgrounded grandchild)
	// must not hold the judge, and its helper slot, past the timeout.
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = claudecli.Environment(nil, cmd.Dir)
	cmd.Stdin = strings.NewReader(judgePrompt(permission, patterns, metadata, customSections))
	out, err := cmd.Output()
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: claude -p failed")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	verdict, reasoning, valid := parseJudgeResponse(string(out))
	return JudgeResult{Verdict: verdict, Reasoning: reasoning, EvaluationFailed: !valid}
}
