package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestBaseResolutionErrors(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		commands        [][]string
	}{
		{"symbolic ref", `[ "$3" = symbolic-ref ]`, nil},
		{"local default", `[ "$7" = 'refs/heads/main^{commit}' ]`, [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}}},
		{"remote default", `[ "$7" = 'refs/remotes/origin/main^{commit}' ]`, [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}, {"branch", "-m", "develop"}}},
		{"current symbolic ref", `[ "$3" = symbolic-ref ] && [ "$5" = HEAD ]`, nil},
		{"current commit", `[ "$7" = 'refs/heads/main^{commit}' ]`, nil},
		{"detached commit", `[ "$7" = 'HEAD^{commit}' ]`, [][]string{{"checkout", "--detach"}}},
		{"Factory named fallback", `[ "$7" = 'refs/heads/main^{commit}' ]`, [][]string{{"checkout", "--detach"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initTestRepo(t)
			for _, args := range tc.commands {
				gitRun(t, repo, args...)
			}
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\nif %s; then exit 128; fi\nexec %q \"$@\"\n", tc.condition, realGit)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if tc.name != "Factory named fallback" {
				if ref, err := ResolveBaseRef(t.Context(), repo); err == nil || ref != "" {
					t.Fatalf("failed probe = %q, %v", ref, err)
				}
			}
			if target, _, err := PrepareFactoryWorkspace(t.Context(), repo, "factory/new", "", ""); err == nil || target != "" {
				t.Fatalf("failed Factory probe = %q, %v", target, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveBaseRef(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled probe = %v", err)
	}
}

func TestResolveBaseRefCreatesWorktree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands [][]string
		want     string
	}{
		{"remote default collides with local branch", [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}, {"branch", "-m", "origin/main"}}, "refs/remotes/origin/main"},
		{"local branch collides with tag", [][]string{{"tag", "main"}}, "refs/heads/main"},
		{"local default", [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}, {"checkout", "-b", "develop"}}, "refs/heads/main"},
		{"remote-only default", [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}, {"branch", "-m", "develop"}}, "refs/remotes/origin/main"},
		{"stale remote HEAD", [][]string{{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/missing"}, {"branch", "-m", "develop"}}, "refs/heads/develop"},
		{"detached HEAD", [][]string{{"checkout", "--detach"}, {"branch", "-D", "main"}}, "HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initTestRepo(t)
			ctx := context.Background()
			for _, args := range tc.commands {
				if out, err := gitexec.Command(ctx, append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("setup %v: %v: %s", args, err, out)
				}
			}
			base, err := ResolveBaseRef(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "session-test", NewBranch: true, BaseRef: base}); err != nil {
				t.Fatalf("create from resolved base %q: %v", base, err)
			}
			if base != tc.want {
				t.Errorf("base = %q, want %q", base, tc.want)
			}
		})
	}
}

func TestResolveBaseRefNoCommits(t *testing.T) {
	repo := t.TempDir()
	ctx := context.Background()
	if out, err := gitexec.Command(ctx, "-C", repo, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	if ref, err := ResolveBaseRef(ctx, repo); ref != "" || err != nil {
		t.Fatalf("empty repository base = %q, %v, want no usable base", ref, err)
	}
}

func TestBaseRefBranch(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"refs/heads/main", "main"},
		{"refs/remotes/origin/main", "main"},
		{"refs/heads/refs/remotes/origin/release", "refs/remotes/origin/release"},
		{"refs/remotes/origin/refs/heads/release", "refs/heads/release"},
		{"main", "main"},
	} {
		if got := BaseRefBranch(tc.ref); got != tc.want {
			t.Errorf("BaseRefBranch(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}
