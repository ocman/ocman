package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/autoapprove"
)

func TestNamedClaudeHelpersUseSelectedAccount(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "env")
	cli := filepath.Join(dir, "claude")
	// A tool-free helper fixture. Never run the installed agent.
	script := "#!/bin/sh\n/usr/bin/env > \"$HELPER_ENV_LOG\"\nprintf '%s' \"$*\" > \"$HELPER_ARGS_LOG\"\ncat >/dev/null\ncase \"$*\" in *--system-prompt*) echo '{\"title\":\"Selected account\"}';; *) echo 'Selected account';; esac\n"
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELPER_ENV_LOG", logPath)
	t.Setenv("HELPER_ARGS_LOG", filepath.Join(dir, "args"))
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited-owner")
	t.Setenv("ANTHROPIC_API_KEY", "owner-secret")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	config := filepath.Join(dir, "selected")
	p := &claudePlatform{cli: cli, configDir: config, slots: filepath.Join(dir, "slots"), connectionAccount: true}
	check := func() {
		t.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		env := string(data)
		if !strings.Contains(env, "CLAUDE_CONFIG_DIR="+config+"\n") || strings.Contains(env, "ANTHROPIC_API_KEY=") || strings.Contains(env, "CLAUDE_CODE_USE_BEDROCK=") {
			t.Fatal("helper used inherited account configuration or auth")
		}
		args, err := os.ReadFile(filepath.Join(dir, "args"))
		if err != nil || !strings.Contains(string(args), "--setting-sources user") {
			t.Fatal("named helper loaded project settings")
		}
	}
	if _, err := p.WorktreeName(t.Context(), dir, "name this"); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := p.GenerateSessionTitle(t.Context(), dir, "title this"); err != nil {
		t.Fatal(err)
	}
	check()
	judge, ok := p.PermissionJudge().(autoapprove.ClaudeJudge)
	if !ok || judge.Environment == nil || judge.SettingsSources != "user" {
		t.Fatal("named judge has no account environment")
	}
	env := strings.Join(judge.Environment(dir), "\n") + "\n"
	if !strings.Contains(env, "CLAUDE_CONFIG_DIR="+config+"\n") || strings.Contains(env, "ANTHROPIC_API_KEY=") {
		t.Fatal("judge selected owner authentication")
	}
}
