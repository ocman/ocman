package git

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestResolveBaseRefCreatesWorktree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands [][]string
		want     string
	}{
		{"local default", [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}, {"checkout", "-b", "develop"}}, "main"},
		{"remote-only default", [][]string{{"update-ref", "refs/remotes/origin/main", "HEAD"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"}, {"branch", "-m", "develop"}}, "origin/main"},
		{"stale remote HEAD", [][]string{{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/missing"}, {"branch", "-m", "develop"}}, "develop"},
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
			base := ResolveBaseRef(ctx, repo)
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
	if ref := ResolveBaseRef(ctx, repo); ref != "" {
		t.Fatalf("empty repository base = %q, want no usable base", ref)
	}
}
