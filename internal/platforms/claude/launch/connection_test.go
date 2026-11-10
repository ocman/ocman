package claudecli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestAccountEnvironment(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_PROFILE", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_FUTURE_OVERRIDE", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_FUTURE_SERVICE", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"} {
		t.Setenv(key, "test-owner-override")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-tool-credential")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("MCP_VARIABLE", "keep=equals")
	before := os.Environ()
	for _, config := range []string{"/account one", "/account two"} {
		values := map[string]string{}
		for _, entry := range AccountEnvironment(config, "/project/worktree") {
			key, value, _ := strings.Cut(entry, "=")
			if _, duplicate := values[key]; duplicate {
				t.Fatalf("duplicate key %s", key)
			}
			values[key] = value
			if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_") || strings.HasPrefix(key, "CLAUDE_CODE_OAUTH_") || key == "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR" {
				t.Fatalf("inherited authentication override %s", key)
			}
		}
		for key, want := range map[string]string{"CLAUDE_CONFIG_DIR": config, "PWD": "/project/worktree", "HOME": os.Getenv("HOME"), "AWS_ACCESS_KEY_ID": "test-tool-credential", "MCP_VARIABLE": "keep=equals", "CLAUDE_CODE_ENTRYPOINT": "sdk-py", "CLAUDE_AGENT_SDK_VERSION": SDKVersion, "CLAUDE_CODE_SDK_READS_SESSION_STATE": "1"} {
			if values[key] != want {
				t.Fatalf("%s = %q, want %q", key, values[key], want)
			}
		}
		if _, exists := values["CLAUDECODE"]; exists {
			t.Fatal("nested Claude marker inherited")
		}
	}
	if !reflect.DeepEqual(before, os.Environ()) {
		t.Fatal("account environment changed the process environment")
	}
}
