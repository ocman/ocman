package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
)

func TestNamedClaudeLaunchCarriesAccountThroughTmux(t *testing.T) {
	e, connections := namedRecoveryEnv(t)
	fixtureCLI := filepath.Join(e.bin, "fixture-claude")
	if err := os.Rename(filepath.Join(e.bin, "claude"), fixtureCLI); err != nil {
		t.Fatal(err)
	}
	// Wrap the shared fixture only to inspect the actual launched child's env.
	// Record selected test variables, never the owner's complete environment.
	cli := "#!/bin/sh\ncase \"$*\" in *--input-format*) printf '%s' \"$*\" > \"$CHILD_ARGS_LOG\"; printf '%s\\n' \"$CLAUDE_CONFIG_DIR\" \"${ANTHROPIC_API_KEY-unset}\" \"${CLAUDE_CODE_USE_BEDROCK-unset}\" > \"$CHILD_ACCOUNT_LOG\";; esac\nexec \"$FIXTURE_CLI\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(e.bin, "claude"), []byte(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(e.data, "child-account")
	t.Setenv("CHILD_ACCOUNT_LOG", log)
	t.Setenv("FIXTURE_CLI", fixtureCLI)
	t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("plain.jsonl"))
	t.Setenv("CHILD_ARGS_LOG", filepath.Join(e.data, "child-args"))
	t.Setenv("CLAUDE_CONFIG_DIR", "/owner-account")
	t.Setenv("ANTHROPIC_API_KEY", "owner-secret")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p, err := newConnectionAt(ctx, connections[1], e.data, e.exe, e.state, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	go func() { _ = p.WatchEvents(ctx) }()
	created, err := p.CreateSession(ctx, platforms.CreateSessionRequest{Directory: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: created.ID, Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "named child account environment", func() bool {
		data, err := os.ReadFile(log)
		return err == nil && strings.Count(string(data), "\n") == 3
	})
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != connections[1].Directory+"\nunset\nunset\n" {
		t.Fatal("tmux child inherited owner account or auth")
	}
	args, err := os.ReadFile(filepath.Join(e.data, "child-args"))
	if err != nil || !strings.Contains(string(args), "--setting-sources user") {
		t.Fatal("named session did not suppress project settings")
	}
}
