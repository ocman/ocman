// Package opencodeskills installs ocman-owned embedded skills for OpenCode.
package opencodeskills

import (
	"fmt"
	"os"
	"path/filepath"
)

// Install extracts skills to XDG data then links them into OpenCode's global
// skill directory. Existing user-owned paths are left untouched.
func Install(skills map[string][]byte) error {
	dataHome := os.Getenv("XDG_DATA_HOME")
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if dataHome == "" || configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
	}
	for name, content := range skills {
		target := filepath.Join(dataHome, "ocman", "opencode", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
		link := filepath.Join(configHome, "opencode", "skills", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		if info, err := os.Lstat(link); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			destination, err := os.Readlink(link)
			if err != nil || destination != filepath.Dir(target) {
				continue
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(filepath.Dir(target), link); err != nil {
			return fmt.Errorf("linking skill %q: %w", name, err)
		}
	}
	return nil
}
