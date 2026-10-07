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
