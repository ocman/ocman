package claudeprovider

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
)

func TestNamedConnectionChildEnvironment(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_PROFILE", "ANTHROPIC_FEDERATION_RULE_ID", "ANTHROPIC_ORGANIZATION_ID", "ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(key, "test-owner-override")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/owner-account")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("PATH", "/test/bin")
	t.Setenv("MCP_TEST_VARIABLE", "keep")
	before := os.Environ()
	config := t.TempDir()
	opts := Options{ConnectionAccount: true, ConfigDir: config}
	values := map[string]string{}
	for _, entry := range opts.childEnv() {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Fatalf("duplicate child environment key %s", key)
		}
		values[key] = value
		if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_") || strings.Contains(key, "OAUTH_TOKEN") || key == "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR" || key == "CLAUDECODE" {
			t.Fatalf("named child inherited owner authentication: %s", key)
		}
	}
	for key, want := range map[string]string{"CLAUDE_CONFIG_DIR": config, "HOME": os.Getenv("HOME"), "PATH": "/test/bin", "MCP_TEST_VARIABLE": "keep", "CLAUDE_CODE_ENTRYPOINT": "sdk-py"} {
		if values[key] != want {
			t.Fatalf("%s = %q, want %q", key, values[key], want)
		}
	}
	if !reflect.DeepEqual(before, os.Environ()) {
		t.Fatal("named account changed the process-global environment")
	}
	// Default machine behavior deliberately retains inherited credentials.
	if !strings.Contains(strings.Join((Options{}).childEnv(), "\n"), "ANTHROPIC_API_KEY=test-owner-override") {
		t.Fatal("legacy child lost inherited credentials")
	}
}

func TestNamedConnectionRefusesImplicitConfigBeforeOpeningIndex(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{"", "relative", file, filepath.Join(t.TempDir(), "missing")} {
		dir := t.TempDir()
		ctx := t.Context()
		_, _, err := newSupervisor(ctx, Options{ConnectionAccount: true, ConfigDir: config, IndexPath: filepath.Join(dir, "index.db")}, filepath.Join(dir, "spool"))
		if err == nil {
			t.Fatalf("accepted unavailable named account %q", config)
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("invalid named account opened persistence: %v %v", entries, readErr)
		}
	}
}

func TestNamedConnectionFreezesCanonicalConfig(t *testing.T) {
	config := t.TempDir()
	link := filepath.Join(t.TempDir(), "account")
	if err := os.Symlink(config, link); err != nil {
		t.Fatal(err)
	}
	inherited := "/other-account"
	opts, err := prepareConnectionOptions(Options{ConnectionAccount: true, ConfigDir: link, ConfigEnv: &inherited})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(config)
	if err != nil || opts.ConfigDir != canonical || opts.ConfigEnv == nil || *opts.ConfigEnv != canonical {
		t.Fatalf("account config not canonical: %+v %v", opts, err)
	}
	inherited = "/retargeted"
	if *opts.ConfigEnv != canonical {
		t.Fatal("caller mutation retargeted the named account")
	}
}

func TestNamedConnectionSessionUsesFilteredEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-owner-override")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", "/other-account")
	config := t.TempDir()
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) {
		o.ConnectionAccount, o.ConfigDir = true, config
	})
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "named child idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	h.live.mu.Lock()
	defer h.live.mu.Unlock()
	p := h.live.procs[id]
	if p == nil {
		t.Fatal("named child missing")
	}
	values := map[string]string{}
	for _, entry := range p.cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if values["ANTHROPIC_API_KEY"] != "" || values["CLAUDE_CODE_USE_BEDROCK"] != "" || values["CLAUDE_CONFIG_DIR"] != h.live.opts.ConfigDir || values["PWD"] != h.dir || values["OCMAN_MANAGED"] != "1" {
		t.Fatal("session spawn bypassed named account environment policy")
	}
}
