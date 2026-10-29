// Package acpexec provides an opt-in ACP execution transport. It does not
// register platforms, ingest history, install adapters or manage host ownership.
package acpexec

import (
	"fmt"

	"github.com/ocman/ocman/internal/platforms"
)

// Command launches an already-installed agent. Args are passed directly to the
// executable, never through a shell or an on-demand package installer.
type Command struct {
	Executable string
	Args       []string
}

// DefaultCommand returns the native ACP command or the separately installed
// ecosystem adapter. Callers may replace it with an explicit executable/args.
func DefaultCommand(platform platforms.ID) (Command, error) {
	switch platform {
	case "opencode":
		return Command{Executable: "opencode", Args: []string{"acp"}}, nil
	case "codex":
		return Command{Executable: "codex-acp"}, nil
	case "claudecode":
		return Command{Executable: "claude-agent-acp"}, nil
	default:
		return Command{}, fmt.Errorf("ACP command for %q: %w", platform, platforms.ErrUnsupported)
	}
}
