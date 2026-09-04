package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFactoryHandoffRequiresCleanSharedBranch(t *testing.T) {
	repo := initTestRepo(t)
	created, err := CreateWorktree(context.Background(), CreateWorktreeRequest{RepoRoot: repo, Branch: "factory/epic-1", NewBranch: true, BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFactoryHandoff(context.Background(), repo, created.Branch); err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("unpushed handoff error = %v", err)
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, repo, "init", "--bare", remote)
	gitRun(t, created.Path, "remote", "add", "origin", remote)
	gitRun(t, created.Path, "push", "-u", "origin", created.Branch)
	if _, err := ValidateFactoryHandoff(context.Background(), repo, created.Branch); err != nil {
		t.Fatalf("clean handoff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(created.Path, "committed.txt"), []byte("committed"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, created.Path, "add", "committed.txt")
	gitRun(t, created.Path, "commit", "-m", "test: committed handoff")
	if _, err := ValidateFactoryHandoff(context.Background(), repo, created.Branch); err == nil || !strings.Contains(err.Error(), "has not been pushed") {
		t.Fatalf("ahead handoff error = %v", err)
	}
	gitRun(t, created.Path, "push")
	if err := os.WriteFile(filepath.Join(created.Path, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFactoryHandoff(context.Background(), repo, created.Branch); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty handoff error = %v", err)
	}
	if _, err := ValidateFactoryHandoff(context.Background(), repo, "factory/missing"); err == nil {
		t.Fatal("missing shared branch was accepted")
	}
}
