package autoapprove

import (
	"os/exec"
	"slices"
	"testing"

	claudecli "github.com/ocman/ocman/internal/platforms/claude/launch"
)

func TestClaudeJudgeSelectedAccountEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/owner")
	t.Setenv("ANTHROPIC_API_KEY", "owner-secret")
	config := t.TempDir()
	selected := ""
	judge := ClaudeJudge{CLI: "unused", Environment: func(directory string) []string {
		selected = directory
		return claudecli.AccountEnvironment(config, directory)
	}, output: func(cmd *exec.Cmd) ([]byte, error) {
		if selected == "" || selected != cmd.Dir || !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR="+config) || slices.Contains(cmd.Env, "ANTHROPIC_API_KEY=owner-secret") {
			t.Error("judge did not use selected account in its neutral directory")
		}
		return []byte(`{"verdict":"safe","reasoning":"Read only."}`), nil
	}}
	if result := judge.JudgeWithCallback(t.Context(), "/project", "bash", []string{"ls"}, nil, nil, nil); result.EvaluationFailed {
		t.Fatalf("judge: %+v", result)
	}
}
