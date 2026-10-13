package gitexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCoveragePublisherKeepsRepositoryIdentity(t *testing.T) {
	caller := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	for _, args := range [][]string{
		{"init", "-b", "main", caller}, {"init", "--bare", remote},
		{"-C", caller, "config", "user.name", "Repository Owner"},
		{"-C", caller, "config", "user.email", "owner@example.invalid"},
		{"-C", caller, "remote", "add", "origin", remote},
		{"-C", caller, "commit", "--allow-empty", "-m", "initial"},
		{"-C", caller, "checkout", "-b", "gh-pages"},
		{"-C", caller, "push", "origin", "gh-pages"},
		{"-C", caller, "checkout", "main"},
	} {
		isolatedGit(t, args...)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "coverage-publish-baseline.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"scripts", "coverage", "bin"} {
		if err := os.Mkdir(filepath.Join(caller, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	scriptPath := filepath.Join(caller, "scripts", "coverage-publish-baseline.sh")
	if err := os.WriteFile(scriptPath, script, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.json", "frontend.json"} {
		if err := os.WriteFile(filepath.Join(caller, "coverage", name), []byte(`{"pct":100}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the script's publication entirely local; no credentials or network
	// access are needed to exercise its real worktree and commit operations.
	wrapper := "#!/bin/sh\nif [ \"$1\" = push ]; then shift; shift; exec \"$REAL_GIT\" push \"$FIXTURE_REMOTE\" \"$@\"; fi\nexec \"$REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(caller, "bin", "git"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(caller, ".git", "config")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "bash", scriptPath)
	cmd.Env = []string{
		"PATH=" + filepath.Join(caller, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"REAL_GIT=" + realGit, "FIXTURE_REMOTE=" + remote,
		"SERVER_URL=https://example.invalid", "REPOSITORY=fixture/repo", "TOKEN=fixture-token", "GITHUB_SHA=12345678",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("publish: %v %s", err, out)
	}
	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("coverage publisher changed repository identity")
	}
	identity := isolatedGit(t, "--git-dir="+remote, "show", "-s", "--format=%an <%ae>|%cn <%ce>", "gh-pages")
	if identity != "forgejo-actions[bot] <forgejo-actions[bot]@noreply.local>|forgejo-actions[bot] <forgejo-actions[bot]@noreply.local>\n" {
		t.Fatalf("publisher identity=%q", identity)
	}
}
