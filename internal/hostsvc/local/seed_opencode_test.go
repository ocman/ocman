package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// seedFixture returns a source checkout with .opencode dependencies and an
// empty worktree root.
func seedFixture(t *testing.T) (repo, worktree string) {
	t.Helper()
	repo, worktree = t.TempDir(), t.TempDir()
	for rel, body := range map[string]string{
		"node_modules/pkg/index.js": "x",
		"package.json":              "{}",
		"package-lock.json":         "{}",
	} {
		p := filepath.Join(repo, ".opencode", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo, worktree
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A checked-out .opencode symlink must not redirect writes outside the worktree.
func TestSeedOpencodeDepsRejectsLinkedConfigDir(t *testing.T) {
	repo, worktree := seedFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(worktree, ".opencode")); err != nil {
		t.Fatal(err)
	}
	seedOpencodeDeps(context.Background(), repo, worktree)
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("wrote through linked .opencode: %v", entries)
	}
}

// Without a checked-out .opencode, OpenCode installs nothing there either.
func TestSeedOpencodeDepsSkipsMissingConfigDir(t *testing.T) {
	repo, worktree := seedFixture(t)
	seedOpencodeDeps(context.Background(), repo, worktree)
	if exists(filepath.Join(worktree, ".opencode")) {
		t.Fatal("created .opencode in a worktree that has none")
	}
}

// Existing entries, dangling links included, are left untouched.
func TestSeedOpencodeDepsPreservesExistingEntries(t *testing.T) {
	repo, worktree := seedFixture(t)
	dst := filepath.Join(worktree, ".opencode")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "lock")
	if err := os.Symlink(outside, filepath.Join(dst, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	seedOpencodeDeps(context.Background(), repo, worktree)
	if exists(outside) {
		t.Fatal("wrote through a dangling destination link")
	}
	if target, err := os.Readlink(filepath.Join(dst, "package-lock.json")); err != nil || target != outside {
		t.Fatalf("existing link replaced: %q, %v", target, err)
	}
	if !exists(filepath.Join(dst, "node_modules", "pkg", "index.js")) {
		t.Fatal("missing entries not seeded")
	}
}

// A failed copy publishes nothing, so OpenCode's own install still runs.
func TestSeedOpencodeDepsFailedCopyLeavesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	repo, worktree := seedFixture(t)
	unreadable := filepath.Join(repo, ".opencode", "node_modules", "pkg", "index.js")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
	dst := filepath.Join(worktree, ".opencode")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	seedOpencodeDeps(context.Background(), repo, worktree)
	if entries, _ := os.ReadDir(dst); len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("partial seed left behind: %v", names)
	}
}
