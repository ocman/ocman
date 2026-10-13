package local

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestFactoryCheckFixtureDoesNotCommitOrCheckoutCallingRepository(t *testing.T) {
	caller := t.TempDir()
	init := exec.Command("git", "init", "-b", "caller", caller)
	init.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	head := filepath.Join(caller, ".git", "HEAD")
	before, err := os.ReadFile(head)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(caller, ".git"))
	fixture := factoryCheckRepo(t)
	after, err := os.ReadFile(head)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("fixture checked out a branch in calling repository")
	}
	if _, err := os.Stat(filepath.Join(caller, ".git", "refs", "heads", "main")); err == nil {
		t.Fatal("fixture committed into calling repository")
	}
	if root, err := gitexec.Output(t.Context(), fixture, "rev-parse", "--show-toplevel"); err != nil {
		t.Fatal(root, err)
	}
}
