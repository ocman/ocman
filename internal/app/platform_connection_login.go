package app

import (
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/internal/platformconnections"
)

func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Commands are displayed for the owner to run. They never contain credentials
// and are never executed by setup inspection or the browser.
func (f connectionFactory) loginCommand(c platformconnections.Connection) string {
	const cleanEnv = `env -i PATH="$PATH" HOME="$HOME" TERM="$TERM" `
	switch c.Kind {
	case "claudecode":
		return cleanEnv + "CLAUDE_CONFIG_DIR=" + shellArgument(c.Directory) + " claude --setting-sources user auth login"
	case "codex":
		return cleanEnv + "CODEX_HOME=" + shellArgument(c.Directory) + " codex login"
	case "opencode":
		cfg := f.openCodeConfig(c)
		return cleanEnv + "OPENCODE_DB=" + shellArgument(cfg.DBPath) + " OPENCODE_CONFIG_DIR=" + shellArgument(cfg.ConfigDir) +
			" XDG_CONFIG_HOME=" + shellArgument(filepath.Join(cfg.StateDir, "config")) +
			" XDG_DATA_HOME=" + shellArgument(cfg.DataDir) + " XDG_STATE_HOME=" + shellArgument(cfg.StateDir) +
			" XDG_CACHE_HOME=" + shellArgument(cfg.CacheDir) + " XDG_RUNTIME_DIR=" + shellArgument(cfg.RuntimeDir) +
			" OPENCODE_CONFIG_CONTENT='' OPENCODE_CONFIG_URL='' opencode auth login"
	}
	return ""
}
