package codexprovider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
)

func TestNamedCodexRejectsProjectConfigBeforeACPLaunch(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(`model_provider="other"`), 0o600); err != nil {
		t.Fatal(err)
	}
	marker, cli := filepath.Join(dir, "launched"), filepath.Join(dir, "fake-acp")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	b := &backend{ctx: t.Context(), opts: Options{ConnectionAccount: true, ACPCommand: acpexec.Command{Executable: cli}}}
	a := newACPExecution(b)
	_, err := a.create(t.Context(), platforms.CreateSessionRequest{Directory: dir})
	if err == nil || !strings.Contains(err.Error(), "project configuration") {
		t.Fatalf("project config not rejected: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("ACP launched before named project policy check")
	}
}

func TestNamedCodexProjectPolicyRechecksAncestors(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := newACPExecution(&backend{opts: Options{ConnectionAccount: true}})
	if err := a.checkConnectionProject(filepath.Join(project, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codex", "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.checkConnectionProject(filepath.Join(project, "nested")); err == nil {
		t.Fatal("new project config was not detected")
	}
	a.b.opts.ConnectionAccount = false
	if err := a.checkConnectionProject(project); err != nil {
		t.Fatal("legacy project config refused")
	}
}
