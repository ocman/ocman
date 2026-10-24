package github

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/testutil"
)

// tokenMachine isolates token discovery from the developer's environment:
// no token variables, an empty config root and a PATH holding only bin.
func tokenMachine(t *testing.T) (bin, config string) {
	t.Helper()
	bin, config = t.TempDir(), t.TempDir()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("PATH", bin)
	return bin, config
}

func writeGhHosts(t *testing.T, config, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(config, "gh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "gh", "hosts.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverToken(t *testing.T) {
	t.Run("environment wins", func(t *testing.T) {
		tokenMachine(t)
		t.Setenv("GH_TOKEN", "env-token")
		if got := discoverToken(); got != "env-token" {
			t.Fatalf("token = %q", got)
		}
	})
	t.Run("gh auth token", func(t *testing.T) {
		bin, config := tokenMachine(t)
		testutil.WriteScript(t, filepath.Join(bin, "gh"), "#!/bin/sh\necho ' command-token '\n")
		writeGhHosts(t, config, "github.com:\n  oauth_token: file-token\n")
		if got := discoverToken(); got != "command-token" {
			t.Fatalf("token = %q", got)
		}
	})
	t.Run("hosts.yml when gh fails", func(t *testing.T) {
		bin, config := tokenMachine(t)
		testutil.WriteScript(t, filepath.Join(bin, "gh"), "#!/bin/sh\nexit 1\n")
		writeGhHosts(t, config, "github.com:\n  oauth_token: file-token\n")
		if got := discoverToken(); got != "file-token" {
			t.Fatalf("token = %q", got)
		}
	})
	t.Run("malformed hosts.yml is skipped", func(t *testing.T) {
		_, config := tokenMachine(t)
		writeGhHosts(t, config, "github.com: [\n")
		if got := discoverToken(); got != "" {
			t.Fatalf("token = %q", got)
		}
	})
	t.Run("anonymous without gh or config", func(t *testing.T) {
		tokenMachine(t)
		if c := New(); c.Token() != "" {
			t.Fatalf("token = %q", c.Token())
		}
	})
}
