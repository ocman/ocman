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

func TestFactoryWorkspaceRemoteOnlyDefault(t *testing.T) {
	repo := initTestRepo(t)
	gitRun(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitRun(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitRun(t, repo, "branch", "-m", "develop")
	target, base, err := PrepareFactoryWorkspace(t.Context(), repo, "factory/new", "", "")
	if err != nil || target != "main" || base != "refs/remotes/origin/main" {
		t.Fatalf("remote-only target = %s/%s, %v", target, base, err)
	}
}

func TestFactoryWorkspaceDetachedDefault(t *testing.T) {
	for _, branch := range []string{"main", "master", "no-named-target"} {
		t.Run(branch, func(t *testing.T) {
			repo := initTestRepo(t)
			gitRun(t, repo, "branch", "-m", branch)
			gitRun(t, repo, "checkout", "--detach")
			target, base, err := PrepareFactoryWorkspace(t.Context(), repo, "factory/new", "", "")
			if branch == "no-named-target" {
				if err == nil || !strings.Contains(err.Error(), "specify a named Factory target") {
					t.Fatalf("missing named target error = %v", err)
				}
			} else if err != nil || target != branch || base != "refs/heads/"+branch {
				t.Fatalf("detached target = %s/%s, %v", target, base, err)
			}
		})
	}
}

func TestFactoryWorkspaceAmbiguousDefault(t *testing.T) {
	repo := initTestRepo(t)
	gitRun(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitRun(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitRun(t, repo, "branch", "-m", "origin/main")
	target, base, err := PrepareFactoryWorkspace(t.Context(), repo, "factory/new", "", "")
	if err != nil || target != "main" || base != "refs/remotes/origin/main" {
		t.Fatalf("ambiguous default = %s/%s, %v", target, base, err)
	}
}

func TestFactoryWorkspaceLocalNamespaceName(t *testing.T) {
	repo := initTestRepo(t)
	gitRun(t, repo, "branch", "release")
	gitRun(t, repo, "branch", "-m", "refs/remotes/origin/release")
	target, base, err := PrepareFactoryWorkspace(t.Context(), repo, "factory/new", "", "")
	if err != nil || target != "refs/remotes/origin/release" || base != "refs/heads/refs/remotes/origin/release" {
		t.Fatalf("local namespace target = %s/%s, %v", target, base, err)
	}
}

func TestFactoryCheckpointAcceptsPatchEquivalentRebase(t *testing.T) {
	ctx := t.Context()
	repo := initTestRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, repo, "init", "--bare", remote)
	gitRun(t, repo, "remote", "add", "origin", remote)
	gitRun(t, repo, "push", "-u", "origin", "main")
	created, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "factory/rebase", NewBranch: true, BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	commit := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", name)
		gitRun(t, dir, "commit", "-m", "test: "+name)
	}
	commit(created.Path, "first.txt", "first")
	commit(created.Path, "second.txt", "second")
	gitRun(t, created.Path, "push", "-u", "origin", created.Branch)
	checkpoint, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, "")
	if err != nil {
		t.Fatal(err)
	}
	commit(repo, "main.txt", "main")
	gitRun(t, created.Path, "rebase", "main")
	gitRun(t, created.Path, "push", "--force", "origin", created.Branch)
	rebased, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, checkpoint)
	if err != nil || rebased == checkpoint {
		t.Fatalf("rebased checkpoint = %q, %v", rebased, err)
	}

	for name, rewrite := range map[string]func(){
		"changed patch": func() {
			commit(created.Path, "second.txt", "resolved differently")
			gitRun(t, created.Path, "reset", "--soft", "HEAD~2")
			gitRun(t, created.Path, "commit", "-m", "test: squashed")
		},
		"dropped commit": func() { gitRun(t, created.Path, "reset", "--hard", "HEAD~1") },
		"discarded work": func() { gitRun(t, created.Path, "reset", "--hard", "main") },
	} {
		t.Run(name, func(t *testing.T) {
			gitRun(t, created.Path, "reset", "--hard", rebased)
			rewrite()
			gitRun(t, created.Path, "push", "--force", "origin", created.Branch)
			if _, err := ValidateFactoryCheckpoint(ctx, repo, created.Branch, checkpoint); err == nil || err.Error() != "factory handoff does not include the accepted checkpoint" {
				t.Fatalf("rewritten checkpoint error = %v", err)
			}
		})
	}
}
