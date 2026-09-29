package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFactoryDiffRedFlags(t *testing.T) {
	ctx := t.Context()
	repo := initTestRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "pkg/a_test.go", "package pkg\n")
	write(repo, "pkg/b_test.go", "package pkg\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "tests")
	created, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "factory/flags", NewBranch: true, BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if path, err := FactoryWorktreePath(ctx, repo, "factory/flags"); err != nil || filepath.Clean(path) != filepath.Clean(created.Path) {
		t.Fatalf("worktree path = %q, %v", path, err)
	}
	if _, err := FactoryWorktreePath(ctx, repo, "factory/missing"); err == nil {
		t.Fatal("missing branch worktree was found")
	}
	if flags, err := FactoryDiffRedFlags(ctx, created.Path, "main"); err != nil || len(flags) != 0 {
		t.Fatalf("clean branch flags = %v, %v", flags, err)
	}
	gitRun(t, created.Path, "rm", "-q", "pkg/a_test.go")
	write(created.Path, "pkg/b_test.go", "package pkg\n\nfunc TestB(t *testing.T) { t.Skip(\"later\") }\n")
	write(created.Path, "web/app.ts", "// eslint-disable-next-line\nexport const x = 1\n")
	write(created.Path, "Makefile", "test:\n\ttrue\n")
	gitRun(t, created.Path, "add", "-A")
	gitRun(t, created.Path, "commit", "-m", "cheat")
	flags, err := FactoryDiffRedFlags(ctx, created.Path, "main")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(flags, "\n")
	for _, want := range []string{"deleted test file pkg/a_test.go", "pkg/b_test.go adds", "t.Skip", "web/app.ts adds", "eslint-disable", "changed check configuration Makefile"} {
		if !strings.Contains(got, want) {
			t.Errorf("flags missing %q:\n%s", want, got)
		}
	}
	if _, err := FactoryDiffRedFlags(ctx, created.Path, "no-such-target"); err == nil {
		t.Fatal("unknown target was accepted")
	}
}

func TestFactoryDiffRedFlagsPrefersRemoteTrackingTarget(t *testing.T) {
	ctx := t.Context()
	repo := initTestRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, repo, "init", "--bare", remote)
	gitRun(t, repo, "remote", "add", "origin", remote)
	gitRun(t, repo, "push", "-u", "origin", "main")
	created, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "factory/base", NewBranch: true, BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// A stale local main that gained an unrelated test deletion must not
	// be attributed to the Factory branch.
	if err := os.WriteFile(filepath.Join(repo, "x_test.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "local only")
	gitRun(t, repo, "rm", "-q", "x_test.go")
	gitRun(t, repo, "commit", "-m", "local only delete")
	if head, dirty, err := FactoryWorktreeState(ctx, created.Path); err != nil || dirty || head == "" {
		t.Fatalf("state = %q %v %v", head, dirty, err)
	}
	flags, err := FactoryDiffRedFlags(ctx, created.Path, "main")
	if err != nil || len(flags) != 0 {
		t.Fatalf("flags = %v, %v", flags, err)
	}
}
