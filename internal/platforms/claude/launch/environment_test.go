package claudecli

import (
	"strings"
	"testing"
)

func TestEnvironment(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
	t.Setenv("PWD", "/old")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	for _, tc := range []struct {
		config                   *string
		dir, wantConfig, wantPWD string
	}{
		{nil, "", "/inherited", "/old"},
		{new(""), "/worktree", "", "/worktree"},
		{new("/chosen"), "/project", "/chosen", "/project"},
	} {
		values := map[string]string{}
		for _, kv := range Environment(tc.config, tc.dir) {
			k, v, _ := strings.Cut(kv, "=")
			if _, duplicate := values[k]; duplicate {
				t.Fatalf("duplicate environment key %s", k)
			}
			values[k] = v
		}
		if _, ok := values["CLAUDECODE"]; ok {
			t.Fatal("nested-Claude marker leaked")
		}
		for k, want := range map[string]string{
			"CLAUDE_CONFIG_DIR": tc.wantConfig, "PWD": tc.wantPWD, "ANTHROPIC_API_KEY": "test-key",
			"CLAUDE_CODE_ENTRYPOINT": "sdk-py", "CLAUDE_AGENT_SDK_VERSION": SDKVersion,
			"CLAUDE_CODE_SDK_READS_SESSION_STATE": "1",
		} {
			if values[k] != want {
				t.Fatalf("%s = %q, want %q", k, values[k], want)
			}
		}
	}
}
