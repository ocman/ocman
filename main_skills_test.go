package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallOcmanSkills(t *testing.T) {
	dataHome := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	userSkill := filepath.Join(configHome, "opencode", "skills", "ocman-workflows")
	if err := os.MkdirAll(userSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installOcmanSkills(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dataHome, "ocman", "opencode", "skills", "ocman-session-splitting", "SKILL.md")
	if content, err := os.ReadFile(target); err != nil || len(content) == 0 {
		t.Fatalf("embedded skill = %q, %v", content, err)
	}
	link := filepath.Join(configHome, "opencode", "skills", "ocman-session-splitting")
	if destination, err := os.Readlink(link); err != nil || destination != filepath.Dir(target) {
		t.Fatalf("skill symlink = %q, %v", destination, err)
	}
	if info, err := os.Lstat(userSkill); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("user skill was replaced: %v", err)
	}
}
