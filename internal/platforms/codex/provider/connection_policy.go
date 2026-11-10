package codexprovider

import (
	"fmt"
	"os"
	"path/filepath"
)

// Named accounts intentionally do not support native project config: its
// provider/profile/auth overrides can replace the selected account. Recheck
// before every create/resume/send, including already loaded sessions.
func (a *acpExecution) checkConnectionProject(directory string) error {
	if !a.b.opts.ConnectionAccount {
		return nil
	}
	if !filepath.IsAbs(directory) {
		return fmt.Errorf("named Codex requires an explicit project directory")
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("named Codex project directory is unavailable")
	}
	for {
		if _, err := os.Lstat(filepath.Join(root, ".codex", "config.toml")); !os.IsNotExist(err) {
			return fmt.Errorf("named Codex cannot run with native project configuration; remove project .codex/config.toml or use the legacy machine integration")
		}
		if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("named Codex cannot inspect project configuration")
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil
		}
		root = parent
	}
}
