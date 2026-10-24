package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiffCacheDropsExpiredEntries(t *testing.T) {
	diffCacheMu.Lock()
	diffCacheStorage = map[string]diffEntry{
		"expired": {v: &Diff{}, fetched: time.Now().Add(-2 * diffCacheTTL)},
	}
	diffCacheMu.Unlock()
	t.Cleanup(func() {
		diffCacheMu.Lock()
		diffCacheStorage = map[string]diffEntry{}
		diffCacheMu.Unlock()
	})

	if got := defaultDiffCache.lookupCached("expired"); got != nil {
		t.Fatal("expired diff cache entry returned a value")
	}
	diffCacheMu.Lock()
	defer diffCacheMu.Unlock()
	if len(diffCacheStorage) != 0 {
		t.Fatalf("expired diff cache entries = %d, want 0", len(diffCacheStorage))
	}
}

// gitInit prepares a fresh repo at dir with a single committed file
// (foo.txt = "hello\n") so subsequent test mutations produce
// non-trivial diffs. Skips the test gracefully if `git` is not in PATH.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// gitRun isolates user gitconfig (hooks, signing) from the setup.
	run := func(args ...string) { t.Helper(); gitRun(t, dir, args...) }
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

// TestGetDiff_WorkingTreeChanges applies one change of each kind to a
// single repo and checks every file's entry.
func TestGetDiff_WorkingTreeChanges(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	write := func(name string, body []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("gone.txt", []byte("bye\n"))
	gitRun(t, dir, "add", "gone.txt")
	gitRun(t, dir, "commit", "-m", "gone")

	write("foo.txt", []byte("hello\nworld\n"))
	// Staged so it shows up as `added`, not `untracked`.
	write("new.txt", []byte("brand new\n"))
	gitRun(t, dir, "add", "new.txt")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	write("untracked.txt", []byte("a\nb\nc\n"))
	// 0x00 byte triggers the binary heuristic.
	write("blob.bin", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05})

	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	files := map[string]DiffFile{}
	for _, f := range d.Files {
		files[f.Path] = f
	}
	if len(d.Files) != 5 || len(files) != 5 {
		t.Fatalf("Files = %+v, want 5 distinct paths", d.Files)
	}

	if f := files["foo.txt"]; f.Status != "modified" || f.Additions != 1 || f.Deletions != 0 ||
		!strings.Contains(f.Diff, "+world") {
		t.Errorf("foo.txt = %+v, want modified +1/-0 with a +world line", f)
	}
	if f := files["new.txt"]; f.Status != "added" || f.Additions != 1 {
		t.Errorf("new.txt = %+v, want added with 1 addition", f)
	}
	if f := files["gone.txt"]; f.Status != "deleted" || f.Deletions == 0 {
		t.Errorf("gone.txt = %+v, want deleted with deletions", f)
	}
	// The synthetic diff should look like a real new-file diff.
	if f := files["untracked.txt"]; f.Status != "untracked" || f.Additions != 3 ||
		!strings.Contains(f.Diff, "new file mode") {
		t.Errorf("untracked.txt = %+v, want untracked +3 with a new-file diff", f)
	}
	if f := files["blob.bin"]; !f.IsBinary || f.Diff != "" {
		t.Errorf("blob.bin = %+v, want IsBinary with no embedded diff", f)
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

// TestGetDiff_UnusualFilenames runs real git over names it decorates:
// a space gets a trailing TAB on the ---/+++ headers, and non-ASCII
// bytes and quotes are C-quoted in diff headers and ls-files output.
func TestGetDiff_UnusualFilenames(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	// Pin git's default so a user-level core.quotePath=false cannot
	// make the quoted cases pass vacuously.
	gitRun(t, dir, "config", "core.quotePath", "true")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tracked := []string{"normal.txt", "with space.txt", "café.txt", `q"uote.txt`, "supprimé.txt"}
	for _, name := range tracked {
		write(name, "one\n")
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "names")
	for _, name := range tracked[:4] {
		write(name, "one\ntwo\n")
	}
	if err := os.Remove(filepath.Join(dir, "supprimé.txt")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"new space.txt", "nouveau é.txt", `new"q.txt`} {
		write(name, "fresh\n")
	}

	d, err := GetDiff(context.Background(), dir, DiffOptions{Force: true})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	type entry struct {
		status    string
		additions int
		deletions int
	}
	got := map[string]entry{}
	for _, f := range d.Files {
		got[f.Path] = entry{f.Status, f.Additions, f.Deletions}
	}
	want := map[string]entry{
		"normal.txt":     {"modified", 1, 0},
		"with space.txt": {"modified", 1, 0},
		"café.txt":       {"modified", 1, 0},
		`q"uote.txt`:     {"modified", 1, 0},
		"supprimé.txt":   {"deleted", 0, 1},
		"new space.txt":  {"untracked", 1, 0},
		"nouveau é.txt":  {"untracked", 1, 0},
		`new"q.txt`:      {"untracked", 1, 0},
	}
	if len(got) != len(want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	for path, w := range want {
		if g, ok := got[path]; !ok || g != w {
			t.Errorf("%q = %+v (present %v), want %+v; all files %v", path, g, ok, w, got)
		}
	}
}

func TestParseUnifiedDiff_QuotedRename(t *testing.T) {
	in := `diff --git "a/caf\303\251.txt" "b/th\303\251.txt"
similarity index 100%
rename from "caf\303\251.txt"
rename to "th\303\251.txt"
`
	files := parseUnifiedDiff(in)
	if len(files) != 1 || files[0].Path != "thé.txt" || files[0].OldPath != "café.txt" {
		t.Fatalf("files = %+v, want thé.txt renamed from café.txt", files)
	}
}

func TestPathsFromDiffHeader(t *testing.T) {
	cases := []struct {
		header, old, new string
	}{
		{"diff --git a/x.txt b/x.txt", "x.txt", "x.txt"},
		{"diff --git a/with space.txt b/with space.txt", "with space.txt", "with space.txt"},
		{`diff --git "a/caf\303\251.txt" "b/caf\303\251.txt"`, "café.txt", "café.txt"},
		{`diff --git a/plain.txt "b/q\"uote.txt"`, "plain.txt", `q"uote.txt`},
		{`diff --git "a/q\"uote.txt" b/plain.txt`, `q"uote.txt`, "plain.txt"},
		{`diff --git "a/unterminated b/x`, "", ""},
		{"diff --git a/x.txt", "", ""},
		{"index 123..456", "", ""},
	}
	for _, tc := range cases {
		old, new := pathsFromDiffHeader(tc.header)
		if old != tc.old || new != tc.new {
			t.Errorf("pathsFromDiffHeader(%q) = %q, %q; want %q, %q", tc.header, old, new, tc.old, tc.new)
		}
	}
}

func TestHunkTrackerStart(t *testing.T) {
	cases := []struct {
		line     string
		ok       bool
		old, new int
	}{
		{"@@ -1,3 +1,2 @@ func x()", true, 3, 2},
		{"@@ -1 +1 @@", true, 1, 1},
		{"@@ -0,0 +1,4 @@", true, 0, 4},
		{"@@@ -1 -1 +1 @@@", false, 0, 0},
		{"@@ -1,3 +1,2", false, 0, 0},
		{"@@ -1,3 @@", false, 0, 0},
		{"@@ -x,3 +1 @@", false, 0, 0},
		{"@@ -1,y +1 @@", false, 0, 0},
		{"@@ -1,-2 +1 @@", false, 0, 0},
		{"+++ b/a.txt", false, 0, 0},
	}
	for _, tc := range cases {
		var h hunkTracker
		if got := h.start(tc.line); got != tc.ok || h.old != tc.old || h.new != tc.new {
			t.Errorf("start(%q) = %v (old %d new %d), want %v (old %d new %d)",
				tc.line, got, h.old, h.new, tc.ok, tc.old, tc.new)
		}
	}
}

// TestParseUnifiedDiff_HeaderLookalikeContent pins that content lines
// inside a hunk are classified by the hunk's line counts, not by their
// prefix: deleting a "--" line yields "---", adding "++i;" yields
// "+++i;", and adding "++ b/evil" yields "+++ b/evil", none of which is
// a file header.
func TestParseUnifiedDiff_HeaderLookalikeContent(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		path      string
		additions int
		deletions int
	}{
		{
			name: "deleted yaml separator",
			in: "diff --git a/a.yaml b/a.yaml\n--- a/a.yaml\n+++ b/a.yaml\n" +
				"@@ -1,3 +1,2 @@\n a: 1\n----\n b: 2\n",
			path: "a.yaml", deletions: 1,
		},
		{
			name: "added pre-increment",
			in: "diff --git a/a.c b/a.c\n--- a/a.c\n+++ b/a.c\n" +
				"@@ -1 +1,2 @@\n int i;\n+++i;\n",
			path: "a.c", additions: 1,
		},
		{
			name: "added line spoofing a new-path header",
			in: "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n" +
				"@@ -1 +1,2 @@\n keep\n+++ b/evil\n",
			path: "a.txt", additions: 1,
		},
		{
			name: "deleted line spoofing an old-path header",
			in: "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n" +
				"@@ -1,2 +1 @@\n keep\n---- a/evil\n",
			path: "a.txt", deletions: 1,
		},
		{
			name: "two hunks with lookalikes and no-newline markers",
			in: "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n" +
				"@@ -1,2 +1,2 @@\n----\n+++++\n title\n" +
				"@@ -9 +9 @@\n--- sql\n\\ No newline at end of file\n+++ sql\n\\ No newline at end of file\n",
			path: "a.md", additions: 2, deletions: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := parseUnifiedDiff(tc.in)
			if len(files) != 1 {
				t.Fatalf("len(files) = %d, want 1", len(files))
			}
			f := files[0]
			if f.Path != tc.path || f.Additions != tc.additions || f.Deletions != tc.deletions {
				t.Errorf("got path=%q +%d -%d, want path=%q +%d -%d",
					f.Path, f.Additions, f.Deletions, tc.path, tc.additions, tc.deletions)
			}
		})
	}
}
