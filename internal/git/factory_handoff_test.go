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

func TestFactoryWorkspaceCheckpoint(t *testing.T) {
	ctx := t.Context()
	repo := initTestRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, repo, "init", "--bare", remote)
	gitRun(t, repo, "remote", "add", "origin", remote)
	gitRun(t, repo, "push", "-u", "origin", "main")
	gitRun(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	target, base, err := PrepareFactoryWorkspace(ctx, repo, "factory/checkpoints", "", "")
	if err != nil || target != "main" || base != "refs/remotes/origin/main" {
		t.Fatalf("new workspace = %s/%s, %v", target, base, err)
	}
	created, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "factory/checkpoints", NewBranch: true, BaseRef: base})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, ""); err == nil {
		t.Fatal("accepted unpushed checkpoint")
	}
	gitRun(t, created.Path, "push", "-u", "origin", created.Branch)
	checkpoint, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, "")
	if err != nil || checkpoint == "" {
		t.Fatalf("first checkpoint = %q, %v", checkpoint, err)
	}
	if target, base, err := PrepareFactoryWorkspace(ctx, repo, created.Branch, checkpoint, "main"); err != nil || target != "main" || base != "" {
		t.Fatalf("reuse = %s/%s, %v", target, base, err)
	}
	gitRun(t, created.Path, "commit", "--allow-empty", "-m", "next issue")
	gitRun(t, created.Path, "push")
	head, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, checkpoint)
	if err != nil || head == checkpoint {
		t.Fatalf("next checkpoint = %q, %v", head, err)
	}
	if _, _, err := PrepareFactoryWorkspace(ctx, repo, created.Branch, checkpoint, "main"); err == nil {
		t.Fatal("accepted unexpected branch advance")
	}
	if _, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, strings.Repeat("f", 40)); err == nil {
		t.Fatal("accepted missing history")
	}
	if err := os.WriteFile(filepath.Join(created.Path, "dirty"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareFactoryWorkspace(ctx, repo, created.Branch, head, "main"); err == nil {
		t.Fatal("accepted dirty workspace")
	}
	for _, tt := range []struct{ branch, checkpoint, target string }{
		{"bad branch", "", "main"}, {"factory/new", "", "bad target"},
		{"factory/missing", head, "main"}, {"factory/new", "", "missing-target"},
	} {
		if _, _, err := PrepareFactoryWorkspace(ctx, repo, tt.branch, tt.checkpoint, tt.target); err == nil {
			t.Fatalf("accepted invalid workspace: %#v", tt)
		}
	}
	gitRun(t, repo, "branch", "local-target")
	if target, base, err := PrepareFactoryWorkspace(ctx, repo, "factory/local", "", "local-target"); err != nil || target != "local-target" || base != "refs/heads/local-target" {
		t.Fatalf("local target = %s/%s, %v", target, base, err)
	}
}
