package git

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzParseUnifiedDiffCounts builds a one-file diff whose hunk deletes and
// adds the fuzzed lines, with a header that counts them, and parses it. The
// +/- totals must match the hunk header and the path must come from the
// file headers: hunk content that looks like a header ("--- a", "+++ b/x")
// is content, classified by its position in the hunk.
func FuzzParseUnifiedDiffCounts(f *testing.F) {
	f.Add("hello\nworld", "bye", "file.txt")
	f.Add("---", "++ b/evil", "a.md")
	f.Add("@@ -1 +1 @@", "-- a/x", "dir/f.go")
	f.Add("diff --git a/x b/x", "", "x")
	f.Add("", "", "empty")
	f.Fuzz(func(t *testing.T, added, deleted, path string) {
		// git quotes unusual paths ("a b", "é", "q\"x"); those headers have
		// their own decoding, so keep to paths git prints verbatim.
		if path == "" || strings.HasPrefix(path, "/") {
			return
		}
		for _, r := range path {
			if r <= ' ' || r > '~' || r == '"' || r == '\\' {
				return
			}
		}
		add, del := diffLines(added), diffLines(deleted)
		var b strings.Builder
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1111111..2222222 100644\n--- a/%s\n+++ b/%s\n", path, path, path, path)
		fmt.Fprintf(&b, "@@ -1,%d +1,%d @@ func f()\n", len(del)+1, len(add)+1)
		b.WriteString(" context\n")
		for _, l := range del {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range add {
			b.WriteString("+" + l + "\n")
		}
		files := parseUnifiedDiff(b.String())
		if len(files) != 1 {
			t.Fatalf("%d files from\n%s", len(files), b.String())
		}
		got := files[0]
		if got.Path != path || got.Status != "modified" || got.IsBinary {
			t.Fatalf("path %q status %q binary %v, want %q modified\n%s", got.Path, got.Status, got.IsBinary, path, b.String())
		}
		if got.Additions != len(add) || got.Deletions != len(del) {
			t.Fatalf("counts +%d -%d, want +%d -%d\n%s", got.Additions, got.Deletions, len(add), len(del), b.String())
		}
	})
}

// diffLines splits fuzzed text into hunk content lines; empty text is none.
func diffLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// checkRefFormatBranch mirrors `git check-ref-format --branch` for a short
// branch name (refs/heads/<name>), per git-check-ref-format(1).
func checkRefFormatBranch(name string) bool {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") {
		return false
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return false
		}
	}
	for _, c := range strings.Split(name, "/") {
		if strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return false
		}
	}
	return true
}

// FuzzSlugs checks both slug functions. SlugForBranch must yield a short,
// idempotent path segment of [a-z0-9._-] that keeps WorktreePathFor inside
// the repository's .worktrees directory. TitleSlug, whose result becomes a
// worktree's branch name, must yield a valid, idempotent git branch name of
// at most 48 characters (or nothing).
func FuzzSlugs(f *testing.F) {
	for _, s := range []string{
		"main", "feature/login", "..", "../../etc", "a//b", "Ünïcødé", strings.Repeat("x/", 80),
		".lock", "-", ".", "x.lock", "Fix the bug", "\n\n  Add: thing", "İstanbul", "ǅ", strings.Repeat("ab ", 40), "---",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		slug := SlugForBranch(s)
		if slug == "" || len(slug) > maxSlugLength {
			t.Fatalf("SlugForBranch(%q) = %q: bad length", s, slug)
		}
		for _, r := range slug {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
				t.Fatalf("SlugForBranch(%q) = %q: bad character %q", s, slug, r)
			}
		}
		if strings.Trim(slug, "-.") != slug || strings.Contains(slug, "--") {
			t.Fatalf("SlugForBranch(%q) = %q: untrimmed or uncollapsed", s, slug)
		}
		if again := SlugForBranch(slug); again != slug {
			t.Fatalf("SlugForBranch not idempotent: %q -> %q -> %q", s, slug, again)
		}
		if p := WorktreePathFor("/home/u/src/repo", s); filepath.Dir(p) != "/home/u/src/.worktrees/repo" {
			t.Fatalf("WorktreePathFor(%q) = %q escapes the worktrees directory", s, p)
		}

		title := TitleSlug(s)
		if title == "" {
			return
		}
		if len(title) > 48 || strings.HasSuffix(title, "-") {
			t.Fatalf("TitleSlug(%q) = %q: too long or trailing dash", s, title)
		}
		if !checkRefFormatBranch(title) {
			t.Fatalf("TitleSlug(%q) = %q: invalid branch name", s, title)
		}
		if again := TitleSlug(title); again != title {
			t.Fatalf("TitleSlug not idempotent: %q -> %q -> %q", s, title, again)
		}
	})
}
