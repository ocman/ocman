package local

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func TestProjectOpencodeRootPlainDirectorySymlink(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := projectOpencodeRoot(t.Context(), alias)
	if err != nil || got != want {
		t.Fatalf("root = %q, %v; want %q", got, err, want)
	}
}

func TestEnsureProjectOpencodeInvalidDirectoryDoesNotLaunch(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", file, filepath.Join(t.TempDir(), "missing")} {
		t.Run(dir, func(t *testing.T) {
			rt := &fakeRuntime{}
			h := New(Deps{Runtime: rt})
			if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir}); err == nil {
				t.Fatal("invalid directory accepted")
			}
			if rt.launchCount() != 0 {
				t.Fatal("invalid directory launched an instance")
			}
		})
	}
}

func TestManagedOpencodeRootMissingDirectoryAndCancellation(t *testing.T) {
	h := New(Deps{Runtime: &fakeRuntime{}})
	dir := filepath.Join(t.TempDir(), "removed")
	if _, err := h.ManagedOpencodeRoot(t.Context(), dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing directory was not classified: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.ManagedOpencodeRoot(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("missing directory masked cancellation: %v", err)
	}
}

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
	rt := &fakeRuntime{}
	h := New(Deps{Runtime: rt})
	got, err := h.ManagedOpencodeRoot(context.Background(), wt)
	if err != nil || got != want {
		t.Fatalf("projectOpencodeRoot = %q, %v; want %q", got, err, want)
	}
	if rt.launchCount() != 0 || rt.stopCount() != 0 {
		t.Fatal("membership lookup mutated runtime")
	}
}
