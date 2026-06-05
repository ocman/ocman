package gitinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// gitInit prepares a fresh repo at dir with a single committed file
// (foo.txt = "hello\n") so subsequent test mutations produce
// non-trivial diffs. Skips the test gracefully if `git` is not in PATH.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(gitexec.CleanEnv(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
			// Avoid user gitconfig leaking into the test (e.g.
			// custom commit hooks, signing config) — those would
			// break the deterministic test setup.
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--initial-branch=main")
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write foo.txt: %v", err)
	}
	run("add", "foo.txt")
	run("commit", "-m", "init")
}

func TestGetDiff_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err == nil {
		t.Fatalf("expected error for non-repo, got nil")
	}
	// Should be ErrNotRepo specifically so the handler can return 404.
	if err.Error() != ErrNotRepo.Error() {
		t.Errorf("error = %v, want ErrNotRepo", err)
	}
}

func TestGetDiff_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if d.Branch != "main" {
		t.Errorf("Branch = %q, want main", d.Branch)
	}
	if len(d.Files) != 0 {
		t.Errorf("expected zero files in clean repo, got %d: %+v", len(d.Files), d.Files)
	}
	// Files must be non-nil so JSON serialises as "[]" not "null".
	// The frontend coerces null defensively but the backend's
	// contract is to ship an array shape.
	if d.Files == nil {
		t.Errorf("Files should be an empty slice, not nil (would marshal to JSON null)")
	}
	if d.Truncated {
		t.Errorf("Truncated should be false")
	}
}

func TestGetDiff_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("Files = %d, want 1: %+v", len(d.Files), d.Files)
	}
	f := d.Files[0]
	if f.Path != "foo.txt" {
		t.Errorf("Path = %q, want foo.txt", f.Path)
	}
	if f.Status != "modified" {
		t.Errorf("Status = %q, want modified", f.Status)
	}
	if f.Additions != 1 || f.Deletions != 0 {
		t.Errorf("counts = %d/%d, want 1/0", f.Additions, f.Deletions)
	}
	if !strings.Contains(f.Diff, "+world") {
		t.Errorf("Diff missing +world line:\n%s", f.Diff)
	}
}

func TestGetDiff_AddedFile_StagedAndUnstaged(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Stage it so it shows up as `added`, not `untracked`.
	cmd := exec.Command("git", "-C", dir, "add", "new.txt")
	cmd.Env = append(gitexec.CleanEnv(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(d.Files))
	}
	if d.Files[0].Status != "added" {
		t.Errorf("Status = %q, want added", d.Files[0].Status)
	}
	if d.Files[0].Additions != 1 {
		t.Errorf("Additions = %d, want 1", d.Files[0].Additions)
	}
}

func TestGetDiff_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.Remove(filepath.Join(dir, "foo.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(d.Files))
	}
	if d.Files[0].Status != "deleted" {
		t.Errorf("Status = %q, want deleted", d.Files[0].Status)
	}
	if d.Files[0].Deletions == 0 {
		t.Errorf("Deletions = 0, want >0")
	}
}

func TestGetDiff_UntrackedFile(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(d.Files))
	}
	f := d.Files[0]
	if f.Path != "new.txt" {
		t.Errorf("Path = %q, want new.txt", f.Path)
	}
	if f.Status != "untracked" {
		t.Errorf("Status = %q, want untracked", f.Status)
	}
	if f.Additions != 3 {
		t.Errorf("Additions = %d, want 3", f.Additions)
	}
	if !strings.Contains(f.Diff, "new file mode") {
		t.Errorf("synthetic diff should look like a real new-file diff:\n%s", f.Diff)
	}
}

func TestGetDiff_BinaryUntrackedNotEmbedded(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	// 0x00 byte triggers the binary heuristic.
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"),
		[]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(d.Files))
	}
	if !d.Files[0].IsBinary {
		t.Errorf("expected IsBinary=true for NUL-containing file")
	}
	if d.Files[0].Diff != "" {
		t.Errorf("binary file Diff should be empty, got %q", d.Files[0].Diff)
	}
}

func TestParseUnifiedDiff_TwoFiles(t *testing.T) {
	// Synthetic diff body with two files; parser should recover
	// both with correct counts.
	in := `diff --git a/a.txt b/a.txt
index 0000..1111 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1,2 @@
 keep
+added
diff --git a/b.txt b/b.txt
index 2222..3333 100644
--- a/b.txt
+++ b/b.txt
@@ -1,2 +1 @@
 keep
-removed
`
	files := parseUnifiedDiff(in)
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].Path != "a.txt" || files[0].Additions != 1 || files[0].Deletions != 0 {
		t.Errorf("a.txt: %+v", files[0])
	}
	if files[1].Path != "b.txt" || files[1].Additions != 0 || files[1].Deletions != 1 {
		t.Errorf("b.txt: %+v", files[1])
	}
}

func TestParseUnifiedDiff_BinaryMarker(t *testing.T) {
	in := `diff --git a/img.png b/img.png
index 0000..1111 100644
Binary files a/img.png and b/img.png differ
`
	files := parseUnifiedDiff(in)
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if !files[0].IsBinary {
		t.Errorf("expected IsBinary=true")
	}
	if files[0].Diff != "" {
		t.Errorf("binary Diff body should be empty")
	}
}

func TestParseUnifiedDiff_Rename(t *testing.T) {
	in := `diff --git a/old.txt b/new.txt
similarity index 100%
rename from old.txt
rename to new.txt
`
	files := parseUnifiedDiff(in)
	if len(files) != 1 {
		t.Fatalf("len(files) = %d", len(files))
	}
	if files[0].Status != "renamed" {
		t.Errorf("Status = %q, want renamed", files[0].Status)
	}
	if files[0].Path != "new.txt" {
		t.Errorf("Path = %q, want new.txt", files[0].Path)
	}
	if files[0].OldPath != "old.txt" {
		t.Errorf("OldPath = %q, want old.txt", files[0].OldPath)
	}
}

func TestIsLikelyBinary(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"empty", []byte{}, false},
		{"plain ascii", []byte("hello\nworld\n"), false},
		{"unicode utf-8", []byte("café résumé\n"), false},
		{"contains nul", []byte{0x68, 0x00, 0x69}, true},
		{"high nonprintable ratio", []byte{
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
			0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15,
		}, true},
	}
	for _, tc := range cases {
		got := isLikelyBinary(tc.in)
		if got != tc.want {
			t.Errorf("%s: isLikelyBinary = %v, want %v", tc.name, got, tc.want)
		}
	}
}
