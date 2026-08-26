package opencodeskills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	dataHome, configHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	userSkill := filepath.Join(configHome, "opencode", "skills", "user-skill")
	if err := os.MkdirAll(userSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(map[string][]byte{"ocman": []byte("skill"), "user-skill": []byte("ignored")}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dataHome, "ocman", "opencode", "skills", "ocman", "SKILL.md")
	if content, err := os.ReadFile(target); err != nil || string(content) != "skill" {
		t.Fatalf("skill = %q, %v", content, err)
	}
	link := filepath.Join(configHome, "opencode", "skills", "ocman")
	if destination, err := os.Readlink(link); err != nil || destination != filepath.Dir(target) {
		t.Fatalf("link = %q, %v", destination, err)
	}
}

func TestRemoveRetiredSkillOnlyUnlinksOcmanOwnedPath(t *testing.T) {
	dataHome, configHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := Install(map[string][]byte{"retired": []byte("skill")}); err != nil {
		t.Fatal(err)
	}
	if err := Remove("retired"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(configHome, "opencode", "skills", "retired")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("owned link still exists: %v", err)
	}
	targetDir := filepath.Join(dataHome, "ocman", "opencode", "skills", "retired")
	if content, err := os.ReadFile(filepath.Join(targetDir, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("extracted data = %q, %v", content, err)
	}

	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Remove("retired"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("user-owned skill removed: %v", err)
	}
}

func TestRemoveRetiredSkillPreservesForeignSymlinkAndData(t *testing.T) {
	dataHome, configHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	targetDir := filepath.Join(dataHome, "ocman", "opencode", "skills", "retired")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(configHome, "opencode", "skills", "retired")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}

	if err := Remove("retired"); err != nil {
		t.Fatal(err)
	}
	if destination, err := os.Readlink(link); err != nil || destination != foreign {
		t.Fatalf("foreign link = %q, %v", destination, err)
	}
	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("unverified data removed: %v", err)
	}
}
