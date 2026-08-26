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
	dataHome, configHome, err := homes()
	if err != nil {
		return err
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

// Remove unlinks a retired skill only when its discovery path is still the
// exact symlink created by Install. Extracted data and user paths are preserved.
func Remove(name string) error {
	dataHome, configHome, err := homes()
	if err != nil {
		return err
	}
	targetDir := filepath.Join(dataHome, "ocman", "opencode", "skills", name)
	link := filepath.Join(configHome, "opencode", "skills", name)
	destination, err := os.Readlink(link)
	if err != nil || destination != targetDir {
		return nil
	}
	return os.Remove(link)
}

func homes() (string, string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if dataHome != "" && configHome != "" {
		return dataHome, configHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	return dataHome, configHome, nil
}
