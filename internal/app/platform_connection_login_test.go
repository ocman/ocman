package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
)

func TestOwnerLoginCommandsQuoteExactConfigurationAndExcludeCredentials(t *testing.T) {
	for _, kind := range []string{"claudecode", "codex", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			bin := t.TempDir()
			cli := map[string]string{"claudecode": "claude", "codex": "codex", "opencode": "opencode"}[kind]
			// These fake commands only print their environment. No native login runs.
			script := "#!/bin/sh\nprintf '%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$CODEX_HOME\" \"$OPENCODE_CONFIG_DIR\" \"$ANTHROPIC_API_KEY\" \"$OPENAI_API_KEY\" \"$OPENCODE_DB\"\nprintf '%s\\n' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, cli), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(t.TempDir(), "account '$HOME;$(false) & config")
			c := platformconnections.Connection{ID: "cnx_reserved", Kind: kind, Directory: directory, Configuration: "isolated"}
			f := connectionFactory{dataDir: t.TempDir()}
			command := f.loginCommand(c)
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", command)
			cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "ANTHROPIC_API_KEY=must-not-leak", "OPENAI_API_KEY=must-not-leak"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("fake sign-in command: %v\n%s", err, out)
			}
			lines := strings.Split(string(out), "\n")
			index := map[string]int{"claudecode": 0, "codex": 1, "opencode": 2}[kind]
			if lines[index] != directory || lines[3] != "" || lines[4] != "" {
				t.Fatalf("configuration/credentials escaped: %q", out)
			}
			if strings.Contains(command, "must-not-leak") {
				t.Fatal("credential in displayed command")
			}
			if kind == "opencode" && lines[5] != f.openCodeConfig(c).DBPath {
				t.Fatal("login used another native database")
			}
		})
	}
	if command := (connectionFactory{}).loginCommand(platformconnections.Connection{Kind: "unknown"}); command != "" {
		t.Fatal(command)
	}
}
