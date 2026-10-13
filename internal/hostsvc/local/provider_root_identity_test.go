package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestProviderRootIncludesArbitraryLinkedWorktreesAndSubdirectories(t *testing.T) {
	repo := initRepo(t)
	linked := filepath.Join(t.TempDir(), "custom-checkout")
	if out, err := gitexec.Command(t.Context(), "-C", repo, "worktree", "add", "-b", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	subdir := filepath.Join(linked, "nested", "directory")
	if err := os.MkdirAll(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{repo, linked, subdir} {
		got, err := ProviderRootForCleanup(t.Context(), directory)
		if err != nil || got != want {
			t.Fatalf("root(%s)=%s err=%v want=%s", directory, got, err, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProviderRootForCleanup(ctx, subdir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled git probe fell back to subdirectory: %v", err)
	}
}

func TestCleanupRootAllowsNonRepositoryButDoesNotGuessAfterFailedProbe(t *testing.T) {
	directory := t.TempDir()
	want, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ProviderRootForCleanup(t.Context(), directory); err != nil || got != want {
		t.Fatal(got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := ProviderRoot(t.Context(), directory); err != nil || got != want {
		t.Fatal("changed non-repository launch behavior", got, err)
	}
	if _, err := ProviderRootForCleanup(t.Context(), directory); err == nil {
		t.Fatal("cleanup guessed after missing git")
	}
}
