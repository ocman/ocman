package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/testutil"
)

func TestPlanProjectClone(t *testing.T) {
	h := New(Deps{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	plan, err := h.PlanProjectClone(t.Context(), hostsvc.ProjectClone{URL: "git@example.com:Org/Repo.git"})
	if err != nil || plan.Directory != filepath.Join(home, "src/example.com/Org/Repo") || plan.URL != "ssh://git@example.com/Org/Repo.git" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(home, "src")); !os.IsNotExist(err) {
		t.Fatal("planning wrote a directory")
	}
	for _, req := range []hostsvc.ProjectClone{
		{URL: "file:///repo"}, {URL: "https://example.com/repo", Directory: "relative"},
		{URL: "https://example.com/repo", Directory: home},
	} {
		if _, err := h.PlanProjectClone(t.Context(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.PlanProjectClone(ctx, hostsvc.ProjectClone{}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func fakeCloneGit(t *testing.T, fail bool) string {
	t.Helper()
	testutil.RequireGit(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
set -eu
case " $* " in
  *" clone "*)
    test "$GIT_ALLOW_PROTOCOL" = https:ssh
    test "$GIT_TERMINAL_PROMPT" = 0
    printf '%s\n' "$*" >> "$CLONE_LOG"
    for arg in "$@"; do prev="${last-}"; last="$arg"; done
    if test "$CLONE_FAIL" = true; then touch "$last/partial"; exit 1; fi
    "$REAL_GIT" init --quiet "$last"
    "$REAL_GIT" -C "$last" remote add origin "$prev"
    ;;
  *) exec "$REAL_GIT" "$@" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REAL_GIT", git)
	t.Setenv("CLONE_LOG", log)
	t.Setenv("CLONE_FAIL", map[bool]string{true: "true", false: "false"}[fail])
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestCloneProjectSuccessAndRetry(t *testing.T) {
	log := fakeCloneGit(t, false)
	t.Setenv("GIT_ALLOW_PROTOCOL", "file") // The clone must override inherited protocol permissions.
	h := New(Deps{})
	req := hostsvc.ProjectClone{URL: "https://example.com/Org/Repo.git", Directory: filepath.Join(t.TempDir(), "nested/repo")}
	for range 2 {
		if err := h.CloneProject(t.Context(), req); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), " clone ") != 1 || !strings.Contains(string(data), "--template= -- ") {
		t.Fatalf("clone calls: %s", data)
	}
	if _, err := h.PlanProjectClone(t.Context(), req); err != nil {
		t.Fatalf("completed clone not reusable: %v", err)
	}
	other := req
	other.URL = "https://example.com/other.git"
	if err := h.CloneProject(t.Context(), other); err == nil {
		t.Fatal("adopted a different project")
	}
}

func TestCloneProjectFailureCleansOnlyReservedDirectory(t *testing.T) {
	fakeCloneGit(t, true)
	h := New(Deps{})
	parent := t.TempDir()
	req := hostsvc.ProjectClone{URL: "https://example.com/repo.git", Directory: filepath.Join(parent, "repo")}
	if err := h.CloneProject(t.Context(), req); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(req.Directory); !os.IsNotExist(err) {
		t.Fatal("partial clone left behind")
	}
	if err := os.Mkdir(req.Directory, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(req.Directory, "keep")
	if err := os.WriteFile(keep, []byte("user work"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.CloneProject(t.Context(), req); err == nil {
		t.Fatal("accepted pre-existing destination")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "user work" {
		t.Fatal("removed user work")
	}
}

func TestCloneProjectRetryWithOwnerURLRewrite(t *testing.T) {
	log := fakeCloneGit(t, false)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.ssh://git@example.com/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://example.com/")
	h := New(Deps{})
	req := hostsvc.ProjectClone{URL: "https://example.com/Org/Repo.git", Directory: filepath.Join(t.TempDir(), "repo")}
	if err := h.CloneProject(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	expanded, err := gitexec.Output(t.Context(), req.Directory, "remote", "get-url", "origin")
	if err != nil || expanded != "ssh://git@example.com/Org/Repo.git" {
		t.Fatalf("rewrite not active: %q, %v", expanded, err)
	}
	if err := h.CloneProject(t.Context(), req); err != nil {
		t.Fatalf("retry rejected a completed clone with an owner-local rewrite: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Count(string(data), " clone ") != 1 {
		t.Fatalf("retry cloned again: %s, %v", data, err)
	}
}

func TestCloneProjectRejectsSymlinkAndProtocolRewrite(t *testing.T) {
	testutil.RequireGit(t)
	h := New(Deps{})
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := h.CloneProject(t.Context(), hostsvc.ProjectClone{URL: "https://example.com/repo", Directory: link}); err == nil {
		t.Fatal("accepted symlink destination")
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file:///nonexistent/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://example.com/")
	req := hostsvc.ProjectClone{URL: "https://example.com/repo", Directory: filepath.Join(root, "repo")}
	if err := h.CloneProject(t.Context(), req); err == nil {
		t.Fatal("allowed file protocol rewrite")
	}
	if _, err := os.Stat(req.Directory); !os.IsNotExist(err) {
		t.Fatal("failed clone not cleaned")
	}
}

func TestProjectUpstreamsCloneURLIsCredentialFree(t *testing.T) {
	repo := initRepo(t)
	if _, err := gitexec.Output(t.Context(), repo, "remote", "add", "origin", "https://user:secret@example.com/Org/Repo.git"); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{ProjectUpstreams: func(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
		return &hostsvc.ProjectUpstreams{RepoRoot: repo}, nil
	}})
	out, err := h.ProjectUpstreams(t.Context(), "/repo")
	if err != nil || out.CloneURL != "https://example.com/Org/Repo.git" {
		t.Fatalf("upstreams=%+v, %v", out, err)
	}
}
