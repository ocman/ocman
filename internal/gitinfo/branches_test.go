package gitinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// gitRun is a small helper mirroring gitInit's runner for test-only
// branch/commit setup.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitexec.CleanEnv(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestListBranches_NonRepo(t *testing.T) {
	got, err := ListBranches(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ListBranches err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("non-repo branches = %v, want empty", got)
	}
}

func TestListBranches_CurrentFirst(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitRun(t, dir, "branch", "feature/a")
	gitRun(t, dir, "branch", "zzz")
	gitRun(t, dir, "checkout", "feature/a")

	got, err := ListBranches(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListBranches err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("branches = %v, want 3", got)
	}
	if got[0] != "feature/a" {
		t.Errorf("current branch not first: %v", got)
	}
}

func TestCheckout_SwitchesBranch(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	gitRun(t, dir, "branch", "other")

	if err := Checkout(context.Background(), dir, "other"); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if b := Lookup(context.Background(), dir).Branch; b != "other" {
		t.Errorf("after checkout branch = %q, want other", b)
	}
}

func TestCheckout_DirtyRejected(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	// Create a second branch that differs in foo.txt so switching would
	// clobber uncommitted changes.
	gitRun(t, dir, "checkout", "-b", "other")
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-am", "other change")
	gitRun(t, dir, "checkout", "main")

	// Dirty the working tree on main so checkout to other is refused.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Checkout(context.Background(), dir, "other")
	if !errors.Is(err, ErrDirtyCheckout) {
		t.Fatalf("Checkout dirty err = %v, want ErrDirtyCheckout", err)
	}
}
