package local

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// A bare repository has no main checkout: its linked worktree keys the
// instance by its own root rather than the bare directory's parent.
func TestProjectOpencodeRoot_BareRepoKeepsWorktreeRoot(t *testing.T) {
	repo := initRepo(t)
	base := t.TempDir()
	bare, wt := filepath.Join(base, "repo.git"), filepath.Join(base, "wt")
	for _, args := range [][]string{
		{"clone", "--bare", repo, bare},
		{"-C", bare, "worktree", "add", "-b", "feature", wt},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = gitexec.CleanEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	want, err := git.ResolveRepoRoot(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := projectOpencodeRoot(context.Background(), wt)
	if err != nil || got != want {
		t.Fatalf("projectOpencodeRoot = %q, %v; want %q", got, err, want)
	}
}
