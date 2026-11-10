package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeNativeConnectionInspection(t *testing.T) {
	for _, tc := range []struct {
		name, version, method, email string
		logged, ready, supported     bool
		exit                         string
	}{
		{"subscription", "2.1.278 (Claude Code)", "claude.ai", "person@example.invalid", true, true, true, "0"},
		{"logged-out", "2.1.268 (Claude Code)", "none", "", false, false, true, "1"},
		{"old", "2.1.220 (Claude Code)", "claude.ai", "person@example.invalid", true, false, false, "0"},
		{"unknown-version", "3.0.0 (Claude Code)", "claude.ai", "person@example.invalid", true, false, false, "0"},
		{"helper", "2.1.278 (Claude Code)", "api_key_helper", "person@example.invalid", true, false, false, "0"},
		{"cloud", "2.1.278 (Claude Code)", "third_party", "person@example.invalid", true, false, false, "0"},
		{"wrong-directory", "2.1.278 (Claude Code)", "claude.ai", "person@example.invalid", true, false, false, "0"},
		{"no-label", "2.1.278 (Claude Code)", "claude.ai", "", true, false, false, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cli := filepath.Join(dir, "claude")
			reported := config
			if tc.name == "wrong-directory" {
				reported = "/other-account"
			}
			data, _ := json.Marshal(map[string]any{"loggedIn": tc.logged, "authMethod": tc.method, "email": tc.email, "configDirectory": reported})
			if err := os.WriteFile(filepath.Join(dir, "status"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '%s|%s|%s|%s\\n' \"$*\" \"$CLAUDE_CONFIG_DIR\" \"${ANTHROPIC_API_KEY-unset}\" \"${CLAUDE_CODE_USE_BEDROCK-unset}\" >> \"$INSPECT_LOG\"\ncase \"$*\" in --version) echo \"$INSPECT_VERSION\";; '--setting-sources user auth status --json') cat \"$INSPECT_STATUS\"; exit \"$INSPECT_EXIT\";; *) exit 98;; esac\n"
			if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("INSPECT_LOG", log)
			t.Setenv("INSPECT_STATUS", filepath.Join(dir, "status"))
			t.Setenv("INSPECT_VERSION", tc.version)
			t.Setenv("INSPECT_EXIT", tc.exit)
			t.Setenv("ANTHROPIC_API_KEY", "owner-secret")
			t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
			t.Setenv("CLAUDE_CONFIG_DIR", "/owner")
			p := &claudePlatform{cli: cli, configDir: config, connectionAccount: true}
			got, err := p.InspectConnection(t.Context())
			if tc.name == "subscription" {
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				got, err = InspectConfiguration(t.Context(), config)
			}
			if err != nil || got.Ready != tc.ready || got.IsolationSupported != tc.supported || got.Message == "" {
				t.Fatalf("inspection: %+v %v", got, err)
			}
			if got.Ready && (got.AccountLabel != tc.email || got.AuthMode != "claude.ai" || got.NativeVersion != "2.1.278") {
				t.Fatalf("identity: %+v", got)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			for _, call := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
				if !strings.HasSuffix(call, "|"+config+"|unset|unset") || strings.Contains(call, "login") || strings.Contains(call, "-p ") {
					t.Fatal("inspection inherited owner auth or made an active call")
				}
			}
		})
	}
}

func TestClaudeInspectionDoesNotInitializeNativeConfig(t *testing.T) {
	config, _ := filepath.EvalSymlinks(t.TempDir())
	p := &claudePlatform{cli: "must-not-launch", configDir: config, connectionAccount: true}
	got, err := p.InspectConnection(t.Context())
	if err != nil || got.Ready || got.IsolationSupported || !strings.Contains(got.Message, "not initialized") {
		t.Fatalf("uninitialized: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(config, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("inspection initialized native account state")
	}
}

func TestClaudeInspectionRejectsNativeAuthOverridesWithoutCLI(t *testing.T) {
	for _, settings := range []string{`{"apiKeyHelper":"do-not-run"}`, `{"env":{"ANTHROPIC_API_KEY":"secret-not-for-output"}}`, `{"profile":"owner"}`, `not-json`} {
		dir := t.TempDir()
		config, _ := filepath.EvalSymlinks(dir)
		if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
		p := &claudePlatform{cli: "never-execute", configDir: config, connectionAccount: true}
		got, err := p.InspectConnection(t.Context())
		if err != nil || got.Ready || got.IsolationSupported || strings.Contains(got.Message, "secret-not-for-output") {
			t.Fatalf("unsafe settings: %+v %v", got, err)
		}
	}
}
