package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchPRHeadRejectsIncompleteRequest(t *testing.T) {
	for _, tc := range []struct {
		repo, remote string
		number       int
	}{
		{"", "origin", 1},
		{"/repo", "", 1},
		{"/repo", "origin", 0},
	} {
		if _, err := FetchPRHead(context.Background(), tc.repo, tc.remote, tc.number); err == nil {
			t.Fatalf("FetchPRHead(%q, %q, %d) succeeded", tc.repo, tc.remote, tc.number)
		}
	}
}

func TestFetchPRHeadCreatesAndUpdatesStableBranch(t *testing.T) {
	root := t.TempDir()
	remote, source, target := filepath.Join(root, "remote.git"), filepath.Join(root, "source"), filepath.Join(root, "target")
	runFetchGit(t, "", "init", "--bare", remote)
	runFetchGit(t, "", "init", source)
	runFetchGit(t, source, "config", "user.email", "test@example.com")
	runFetchGit(t, source, "config", "user.name", "Test")
	runFetchGit(t, "", "init", target)
	runFetchGit(t, target, "remote", "add", "origin", remote)

	var latest string
	for _, content := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		runFetchGit(t, source, "add", "file.txt")
		runFetchGit(t, source, "commit", "-m", content)
		latest = runFetchGit(t, source, "rev-parse", "HEAD")
		runFetchGit(t, source, "push", "--force", remote, "HEAD:refs/pull/7/head")

		branch, err := FetchPRHead(context.Background(), target, "origin", 7)
		if err != nil {
			t.Fatal(err)
		}
		if got := runFetchGit(t, target, "rev-parse", "refs/heads/"+branch); got != latest || branch != "ocman/pr-7" {
			t.Fatalf("branch = %q at %q, want ocman/pr-7 at %q", branch, got, latest)
		}
	}
}

func runFetchGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestFetchPRHeadDoesNotExposeGitOutput(t *testing.T) {
	bin := t.TempDir()
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\necho credential-secret >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, err := FetchPRHead(context.Background(), "/repo", "origin", 7)
	if err == nil || strings.Contains(err.Error(), "credential-secret") {
		t.Fatalf("error = %v, want sanitized failure", err)
	}
}
