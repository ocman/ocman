package claudecode

import (
	"path/filepath"
	"testing"

	claudeprovider "github.com/ocman/ocman/internal/platforms/claude/provider"
)

func TestNamedClaudeProjectOptionsKeepOwnerBudgetsAndScopedWrites(t *testing.T) {
	owner, account, config := t.TempDir(), t.TempDir(), t.TempDir()
	opts := claudeProjectOptions(providerArgs{DataDir: account, OwnerDataDir: owner, ConfigEnv: &config, ConnectionAccount: true, Repo: "/project"})
	if !opts.ConnectionAccount || opts.ConfigDir != config || opts.ConfigEnv == nil || *opts.ConfigEnv != config || !opts.NoSync || opts.ProjectRoot != "/project" {
		t.Fatal("named account configuration not carried to project supervisor")
	}
	legacy := claudeprovider.DefaultOptions(owner)
	if opts.SlotDir != legacy.SlotDir || opts.MCPTokenPath != legacy.MCPTokenPath {
		t.Fatal("owner budget or MCP token split by account")
	}
	for _, path := range []string{opts.StatePath, opts.UsagePath, opts.IndexPath, opts.LaunchPath} {
		if filepath.Dir(path) != account {
			t.Fatalf("account write escaped scope: %s", path)
		}
	}
}
