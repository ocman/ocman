package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
	in := `worktree /home/user/repo
HEAD abcd1234
branch refs/heads/main

worktree /home/user/.worktrees/repo/feature-x
HEAD ef567890
branch refs/heads/feature/x

worktree /home/user/repo-bare
HEAD aaaa1111
bare

worktree /home/user/.worktrees/repo/detached
HEAD bbbb2222
detached

worktree /home/user/.worktrees/repo/locked
HEAD cccc3333
branch refs/heads/locked
locked

`
	got, err := parseWorktreeList(in)
	if err != nil {
		t.Fatalf("parseWorktreeList: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d entries, want 5", len(got))
	}

	// First entry — main
	if got[0].Path != "/home/user/repo" || got[0].Branch != "main" || !got[0].Main {
		t.Errorf("entry 0 = %+v, want main worktree", got[0])
	}

	// Second — feature branch
	if got[1].Branch != "feature/x" {
		t.Errorf("entry 1 branch = %q, want feature/x", got[1].Branch)
	}
	if got[1].Main {
		t.Errorf("entry 1 should not be marked Main")
	}

	// Third — bare
	if !got[2].Bare {
		t.Errorf("entry 2 should be bare")
	}

	// Fourth — detached
	if got[3].Branch != "" {
		t.Errorf("entry 3 branch = %q, want empty for detached", got[3].Branch)
	}

	// Fifth — locked
	if !got[4].Locked {
		t.Errorf("entry 4 should be locked")
	}
}

func TestList(t *testing.T) {
	repo := initTestRepo(t)
	got, err := List(context.Background(), repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1 (just the main checkout)", len(got))
	}
	if got[0].Path != repo {
		t.Errorf("entry 0 path = %q, want %q", got[0].Path, repo)
	}
	if got[0].Branch != "main" {
		t.Errorf("entry 0 branch = %q, want main", got[0].Branch)
	}
	if !got[0].Main {
		t.Errorf("entry 0 should be marked Main")
	}
}

func TestCreate_NewBranch(t *testing.T) {
	repo := initTestRepo(t)
	parent := filepath.Dir(repo)

	res, err := Create(context.Background(), CreateRequest{
		RepoRoot:  repo,
		Branch:    "feature/login",
		NewBranch: true,
		BaseRef:   "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Reused {
		t.Errorf("Create reported Reused=true on first run")
	}
	if res.Branch != "feature/login" {
		t.Errorf("res.Branch = %q, want feature/login", res.Branch)
	}
	wantPath := filepath.Join(parent, ".worktrees", filepath.Base(repo), "feature-login")
	if res.Path != wantPath {
		t.Errorf("res.Path = %q, want %q", res.Path, wantPath)
	}

	// The worktree dir must exist on disk and contain a .git file/dir.
	if _, err := os.Stat(filepath.Join(res.Path, ".git")); err != nil {
		t.Errorf("worktree .git missing: %v", err)
	}

	// And `git worktree list` must report two entries now.
	list, err := List(context.Background(), repo)
	if err != nil {
		t.Fatalf("List after create: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("after create, got %d entries, want 2", len(list))
	}
}

func TestCreate_Idempotent(t *testing.T) {
	repo := initTestRepo(t)

	// First create.
	if _, err := Create(context.Background(), CreateRequest{
		RepoRoot:  repo,
		Branch:    "feature/x",
		NewBranch: true,
		BaseRef:   "main",
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Same call again — must reuse, not error.
	res, err := Create(context.Background(), CreateRequest{
		RepoRoot:  repo,
		Branch:    "feature/x",
		NewBranch: true,
		BaseRef:   "main",
	})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if !res.Reused {
		t.Errorf("second Create reported Reused=false; want true")
	}
}

func TestCreate_BranchAlreadyCheckedOutElsewhere(t *testing.T) {
	repo := initTestRepo(t)

	// Create a worktree for branch "shared" at one path.
	if _, err := Create(context.Background(), CreateRequest{
		RepoRoot:  repo,
		Branch:    "shared",
		NewBranch: true,
		BaseRef:   "main",
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Now manually create a *different* directory to host another
	// attempt at the same branch — the slug-derived path is the same,
	// so we'd hit the idempotent reuse. Simulate the
	// "branch checked out elsewhere" case by creating a worktree
	// directly and then trying to add a *different-slugged* branch
	// pointing to the same ref.
	//
	// Easiest reproduction: try to add the branch "shared" at a
	// hand-picked, different path. Run `git worktree add` directly
	// to bypass our PathFor.
	parent := filepath.Dir(repo)
	otherPath := filepath.Join(parent, "manual-shared")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", otherPath, "shared")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err == nil {
		// Some git versions allow this; in that case the test isn't
		// applicable and we skip rather than fail.
		t.Skipf("git allowed two worktrees on the same branch; skipping. Output: %s", out)
	}

	// Now call our Create — but PathFor("shared") matches the
	// existing path so it'll still reuse. We need a different branch
	// name that slugs to a fresh path but conflicts on the branch
	// in git's eyes. Use a different branch name that points at the
	// same checked-out branch via Create with NewBranch=false on a
	// branch already in another worktree.
	//
	// Simulate by manually creating *another* worktree on a new
	// branch and then pointing Create at it.
	_ = exec.Command("git", "-C", repo, "branch", "another", "main").Run()
	if err := exec.Command("git", "-C", repo, "worktree", "add",
		filepath.Join(parent, "manual-another"), "another").Run(); err != nil {
		t.Fatalf("seed second worktree: %v", err)
	}

	// Try to attach branch "another" at our deterministic path —
	// must fail with ErrBranchCheckedOutElsewhere.
	_, err = Create(context.Background(), CreateRequest{
		RepoRoot:  repo,
		Branch:    "another",
		NewBranch: false,
	})
	if err == nil {
		t.Fatalf("Create returned nil error; want ErrBranchCheckedOutElsewhere")
	}
	if !errors.Is(err, ErrBranchCheckedOutElsewhere) {
		t.Errorf("Create error = %v, want ErrBranchCheckedOutElsewhere", err)
	}
}

func TestCreate_NewBranchButBranchAlreadyExists(t *testing.T) {
	repo := initTestRepo(t)

	// Pre-create a local branch with no associated worktree. This
	// is the case where `git worktree add -b <name>` would refuse
	// ("fatal: a branch named 'X' already exists"). We expect
	// Create to detect this and fall back to a plain checkout.
	if err := exec.Command("git", "-C", repo, "branch", "preexisting", "main").Run(); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	res, err := Create(context.Background(), CreateRequest{
		RepoRoot:  repo,
		Branch:    "preexisting",
		NewBranch: true,
		BaseRef:   "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.BranchExisted {
		t.Errorf("res.BranchExisted = false, want true")
	}
	if res.Reused {
		t.Errorf("res.Reused = true, want false (the *worktree* did not pre-exist)")
	}
	if res.Branch != "preexisting" {
		t.Errorf("res.Branch = %q, want preexisting", res.Branch)
	}

	// The worktree dir must exist on disk now.
	if _, err := os.Stat(filepath.Join(res.Path, ".git")); err != nil {
		t.Errorf("worktree .git missing: %v", err)
	}

	// And `git worktree list` must report two entries.
	list, err := List(context.Background(), repo)
	if err != nil {
		t.Fatalf("List after create: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("after create, got %d entries, want 2", len(list))
	}
}

func TestBranchExists(t *testing.T) {
	repo := initTestRepo(t)
	if !branchExists(context.Background(), repo, "main") {
		t.Errorf("branchExists(main) = false, want true")
	}
	if branchExists(context.Background(), repo, "does-not-exist") {
		t.Errorf("branchExists(does-not-exist) = true, want false")
	}
	// Non-repo dirs should return false rather than panic.
	if branchExists(context.Background(), t.TempDir(), "main") {
		t.Errorf("branchExists on non-repo returned true; want false")
	}
}

func TestResolveBaseRef(t *testing.T) {
	repo := initTestRepo(t)

	// No origin/HEAD, no upstream, current branch is "main" — so
	// resolver should return "main".
	got := ResolveBaseRef(context.Background(), repo)
	if got != "main" {
		t.Errorf("ResolveBaseRef = %q, want main", got)
	}
}

func TestResolveBaseRef_FallsBackToMain(t *testing.T) {
	// Pass a non-repo dir; should still return "main" as the last-
	// resort sentinel rather than erroring out.
	got := ResolveBaseRef(context.Background(), t.TempDir())
	if got != "main" {
		t.Errorf("ResolveBaseRef on non-repo = %q, want main", got)
	}
}
