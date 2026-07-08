package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// Diff is the structured response of a `git diff HEAD` for a working
// directory plus its untracked files. Mirrors the wire shape returned
// by /api/git/diff.
//
// The intent is to give the UI everything it needs to render a per-file
// view (filename, status icon, additions/deletions, and the unified
// diff itself) without further git invocations.
type Diff struct {
	// Repo is the absolute path to the worktree root (output of
	// `git -C <dir> rev-parse --show-toplevel`). May differ from
	// the request's `dir` when dir is a subdirectory.
	Repo string `json:"repo"`
	// Branch is the short branch name. "(detached)" when HEAD is
	// detached. Empty if the diff was somehow obtained without a
	// branch (shouldn't happen post-init).
	Branch string `json:"branch"`
	// Ahead/Behind track the configured upstream, like
	// Info. Zero when no upstream is set.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// Files lists every changed file (modified / added / deleted /
	// renamed) plus every untracked file the worktree carries.
	Files []DiffFile `json:"files"`
	// Truncated is set when output exceeded the size cap and one or
	// more files were dropped from the response. The UI surfaces
	// this as a banner so users know the picture is incomplete.
	Truncated bool `json:"truncated"`
}

// DiffFile is one entry in Diff.Files. Either Diff is a unified diff
// hunk for the file (modified/added/deleted/renamed), or — for
// untracked files — IsBinary is false and Diff carries an "all-
// additions" synthetic hunk produced from the untracked file's
// contents (subject to UntrackedSizeCap). For binary files Diff is
// always empty and IsBinary=true.
type DiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`            // modified|added|deleted|renamed|untracked
	OldPath   string `json:"oldPath,omitempty"` // present for renames
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Diff      string `json:"diff"`
	IsBinary  bool   `json:"isBinary"`
}

// DiffOptions controls a Diff() call.
type DiffOptions struct {
	// Force bypasses the in-process cache. Use when the caller has
	// reason to believe the worktree just changed (e.g. an SSE-
	// driven refetch from the frontend).
	Force bool
}

// Size caps. Per-file cap stops a single huge diff from dwarfing the
// rest; total cap stops a generated-files explosion. Both expressed
// in bytes of the diff body (we don't try to count metadata).
const (
	perFileDiffCap   = 200 * 1024
	totalDiffCap     = 2 * 1024 * 1024
	untrackedSizeCap = 200 * 1024
)

// gitDiffTimeout bounds each `git diff` call. Larger than the
// `git status` budget in info.go because diffs are heavier.
const gitDiffTimeout = 4 * time.Second

// truncationMarker is appended to per-file diffs that exceeded
// perFileDiffCap, so the frontend can show a "(truncated)" indicator
// and the user knows the displayed diff is incomplete.
const truncationMarker = "\n... (file diff truncated)\n"

// diffCacheTTL is how long a successful Diff result is reused. Kept
// short so the SSE-driven refetch path stays responsive — the cache
// only exists to coalesce simultaneous requests (multiple browser
// tabs, race between an SSE event and a manual refresh).
const diffCacheTTL = 1 * time.Second

// diffCache is a separate cache from the Lookup cache: it
// stores Diff values, has a much shorter TTL, and accepts a Force
// override. Reusing the existing cache type would mean cross-
// pollinating two unrelated lifetimes.
type diffCache struct{}

func (d *diffCache) get(ctx context.Context, dir string, force bool) (*Diff, error) {
	if force {
		return runDiff(ctx, dir)
	}
	// Re-use the Lookup cache shape: stash the *Diff in a
	// type-asserted holder via a per-call closure. Cheap.
	cached := d.lookupCached(dir)
	if cached != nil {
		return cached, nil
	}
	out, err := runDiff(ctx, dir)
	if err != nil {
		return nil, err
	}
	d.put(dir, out)
	return out, nil
}

// Internal trivial cache; we don't reuse the existing `cache` type
// because its value type is `Info`, not `*Diff`. Keeping a tiny
// dedicated map is clearer than introducing generics.
type diffEntry struct {
	v       *Diff
	fetched time.Time
}

var (
	diffCacheMu      sync.Mutex
	diffCacheStorage = map[string]diffEntry{}
)

func (d *diffCache) lookupCached(dir string) *Diff {
	diffCacheMu.Lock()
	defer diffCacheMu.Unlock()
	e, ok := diffCacheStorage[dir]
	if !ok {
		return nil
	}
	if time.Since(e.fetched) > diffCacheTTL {
		return nil
	}
	return e.v
}

func (d *diffCache) put(dir string, v *Diff) {
	diffCacheMu.Lock()
	defer diffCacheMu.Unlock()
	diffCacheStorage[dir] = diffEntry{v: v, fetched: time.Now()}
}

var defaultDiffCache = &diffCache{}

// GetDiff returns the working-tree diff for dir. dir must be an
// absolute path. Returns ErrNotRepo if dir isn't (inside) a git
// worktree, or a wrapped exec error if `git` itself failed.
//
// Successful results are cached for diffCacheTTL; pass
// DiffOptions{Force: true} to bypass the cache.
func GetDiff(ctx context.Context, dir string, opts DiffOptions) (*Diff, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("git: dir must be absolute: %q", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("git: dir not accessible: %w", err)
	}
	return defaultDiffCache.get(ctx, dir, opts.Force)
}

// ErrNotRepo is returned when GetDiff is called against a directory
// that isn't inside a git worktree. Handlers translate this into
// HTTP 404. Distinct from a generic error so the handler can decide
// the right status code.
var ErrNotRepo = fmt.Errorf("git: not a git worktree")

// runDiff is the unmemoised core. Exposed for tests (test passes a
// fixture directory).
func runDiff(ctx context.Context, dir string) (*Diff, error) {
	cctx, cancel := context.WithTimeout(ctx, gitDiffTimeout)
	defer cancel()

	repo, err := gitOutput(cctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNotRepo
	}
	repo = strings.TrimSpace(repo)

	branch, ahead, behind := readBranchSummary(cctx, dir)

	// `git diff HEAD --no-color --no-ext-diff` covers staged + unstaged.
	// `--find-renames` keeps the response compact when files moved.
	diffOut, err := gitOutput(cctx, dir,
		"diff", "HEAD",
		"--no-color", "--no-ext-diff",
		"--find-renames",
	)
	if err != nil {
		// HEAD may not exist (fresh repo). Fall back to diff
		// against an empty tree using the standard empty-tree
		// hash. Failing to do this would make the endpoint return
		// 502 on every brand-new project.
		emptyTree, _ := gitOutput(cctx, dir, "hash-object", "-t", "tree", "/dev/null")
		emptyTree = strings.TrimSpace(emptyTree)
		if emptyTree == "" {
			emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904" // git's well-known empty tree
		}
		diffOut, err = gitOutput(cctx, dir,
			"diff", emptyTree,
			"--no-color", "--no-ext-diff",
			"--find-renames",
		)
		if err != nil {
			return nil, fmt.Errorf("git diff: %w", err)
		}
	}
	files := parseUnifiedDiff(diffOut)

	untrackedOut, _ := gitOutput(cctx, dir,
		"ls-files", "--others", "--exclude-standard",
	)
	for _, line := range strings.Split(untrackedOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := buildUntrackedFile(repo, line)
		files = append(files, f)
	}

	// Apply size caps: per-file truncation done in parseUnifiedDiff /
	// buildUntrackedFile; here we enforce the global cap by dropping
	// trailing files once we exceed it.
	out := &Diff{
		Repo:   repo,
		Branch: branch,
		Ahead:  ahead,
		Behind: behind,
		// Always non-nil so json.Marshal emits "[]" not "null". The
		// frontend's hooks and renderer expect an array shape.
		Files: []DiffFile{},
	}
	total := 0
	for _, f := range files {
		total += len(f.Diff)
		if total > totalDiffCap {
			out.Truncated = true
			break
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

// readBranchSummary parses `git status --porcelain=v2 --branch` to
// recover branch + ahead/behind in one shot. Failures degrade to
// zero values rather than failing the whole diff — the diff itself
// is the user-visible payload.
func readBranchSummary(ctx context.Context, dir string) (string, int, int) {
	out, err := gitOutput(ctx, dir,
		"status", "--porcelain=v2", "--branch",
	)
	if err != nil {
		return "", 0, 0
	}
	info := parsePorcelainV2(out)
	return info.Branch, info.Ahead, info.Behind
}

// gitOutput runs `git -C dir <args...>` and returns stdout.
// stderr is discarded; failure is signalled by a non-nil error.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return gitexec.Output(ctx, dir, args...)
}

// parseUnifiedDiff splits a multi-file `git diff` output into per-file
// records. Recognises:
//   - `diff --git a/<old> b/<new>`               start of a file entry
//   - `new file mode ...` / `deleted file mode` / `rename ...`
//   - `Binary files ... differ`                   IsBinary=true
//   - `--- a/<path>` / `+++ b/<path>`             headers
//   - `@@ -A,B +C,D @@ ...` hunk headers
//   - `+...` / `-...` content lines (counted)
//
// The body for each file is the verbatim unified-diff slice between
// successive `diff --git` markers, capped at perFileDiffCap.
func parseUnifiedDiff(s string) []DiffFile {
	if s == "" {
		return nil
	}
	var files []DiffFile
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "diff --git ") {
			i++
			continue
		}
		// Find the end of this file's section: next "diff --git " or EOF.
		start := i
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "diff --git ") {
			j++
		}
		section := lines[start:j]
		f := parseFileSection(section)
		files = append(files, f)
		i = j
	}
	return files
}

// parseFileSection parses one `diff --git ...` block. The status
// detection is best-effort but covers the common cases git emits.
func parseFileSection(section []string) DiffFile {
	header := section[0]
	// Default path: pull from `diff --git a/<old> b/<new>`. The
	// `+++ b/<path>` header below typically agrees with `b/<new>`
	// but the `diff --git` line is more reliable for renames.
	oldPath, newPath := pathsFromDiffHeader(header)
	f := DiffFile{
		Path:    newPath,
		OldPath: "",
		Status:  "modified",
	}

	bodyLines := []string{header}
	bodyByteCount := len(header) + 1
	truncated := false

	for k := 1; k < len(section); k++ {
		line := section[k]
		switch {
		case strings.HasPrefix(line, "new file mode "):
			f.Status = "added"
		case strings.HasPrefix(line, "deleted file mode "):
			f.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			f.Status = "renamed"
			f.OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			f.Status = "renamed"
			f.Path = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "Binary files "):
			f.IsBinary = true
		case strings.HasPrefix(line, "+++ b/"):
			// Trust the +++ header for the new path; corrects
			// edge cases like spaces (git escapes them in
			// `diff --git`).
			f.Path = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/"):
			oldPath = strings.TrimPrefix(line, "--- a/")
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			f.Additions++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			f.Deletions++
		}
		// Per-file truncation. We still keep counting +/- after
		// the cap so the totals reflect reality even when the
		// rendered hunk is partial.
		if !truncated {
			bodyByteCount += len(line) + 1
			if bodyByteCount > perFileDiffCap {
				bodyLines = append(bodyLines, truncationMarker)
				truncated = true
			} else {
				bodyLines = append(bodyLines, line)
			}
		}
	}
	// For renames the *new* path is what the UI primarily shows;
	// preserve the old one for context.
	if f.Status == "renamed" && f.OldPath == "" {
		f.OldPath = oldPath
	}
	if f.IsBinary {
		// Don't ship the synthetic body for binaries — it's all
		// metadata, no useful diff.
		f.Diff = ""
	} else {
		f.Diff = strings.Join(bodyLines, "\n")
	}
	return f
}

// pathsFromDiffHeader extracts (oldPath, newPath) from a
// `diff --git a/<x> b/<y>` line. Falls back to empty strings on
// malformed input. Spaces in paths are escaped by git as e.g.
// `"a/with space"`; we don't try to unquote here — the +++/---
// headers are the authoritative source for the final paths.
func pathsFromDiffHeader(header string) (string, string) {
	const prefix = "diff --git "
	if !strings.HasPrefix(header, prefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(header, prefix)
	// Naive split on " b/": works for most cases. Edge cases
	// (paths containing literal " b/") are rare enough to live
	// with for v1.
	idx := strings.Index(rest, " b/")
	if idx <= 2 {
		return "", ""
	}
	a := strings.TrimPrefix(rest[:idx], "a/")
	b := rest[idx+len(" b/"):]
	return a, b
}

// buildUntrackedFile returns a DiffFile for an untracked path. We
// synthesise a unified diff against /dev/null so the frontend can
// reuse the same renderer; for files over untrackedSizeCap or for
// detected binary content we set IsBinary=true and emit no body.
func buildUntrackedFile(repoRoot, relPath string) DiffFile {
	abs := filepath.Join(repoRoot, relPath)
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		return DiffFile{Path: relPath, Status: "untracked"}
	}
	if st.Size() > untrackedSizeCap {
		return DiffFile{
			Path: relPath, Status: "untracked",
			IsBinary: true, // treat over-cap as "won't show body"
		}
	}
	body, err := os.ReadFile(abs)
	if err != nil {
		return DiffFile{Path: relPath, Status: "untracked"}
	}
	if isLikelyBinary(body) {
		return DiffFile{Path: relPath, Status: "untracked", IsBinary: true}
	}
	text := string(body)
	lines := strings.Split(text, "\n")
	additions := len(lines)
	if strings.HasSuffix(text, "\n") {
		additions--
	}
	if additions < 0 {
		additions = 0
	}
	// Fabricate a unified-diff body so RawDiffView can render it
	// the same way as a real `git diff` hunk.
	var b strings.Builder
	b.WriteString("diff --git a/")
	b.WriteString(relPath)
	b.WriteString(" b/")
	b.WriteString(relPath)
	b.WriteString("\nnew file mode 100644\n")
	b.WriteString("--- /dev/null\n+++ b/")
	b.WriteString(relPath)
	b.WriteString("\n@@ -0,0 +1,")
	fmt.Fprintf(&b, "%d", additions)
	b.WriteString(" @@\n")
	for _, ln := range lines {
		if ln == "" && !strings.HasSuffix(text, "\n") {
			continue
		}
		b.WriteString("+")
		b.WriteString(ln)
		b.WriteString("\n")
	}
	bodyStr := b.String()
	if len(bodyStr) > perFileDiffCap {
		bodyStr = bodyStr[:perFileDiffCap] + truncationMarker
	}
	return DiffFile{
		Path:      relPath,
		Status:    "untracked",
		Additions: additions,
		Diff:      bodyStr,
	}
}

// isLikelyBinary returns true when the byte slice contains a NUL or
// has too high a fraction of non-printable bytes. Same heuristic git
// itself uses for the "Binary files differ" decision (approximated).
func isLikelyBinary(b []byte) bool {
	const sample = 8000
	if len(b) > sample {
		b = b[:sample]
	}
	nonprintable := 0
	for _, c := range b {
		if c == 0 {
			return true
		}
		if c < 9 || (c > 13 && c < 32) {
			nonprintable++
		}
	}
	if len(b) == 0 {
		return false
	}
	return nonprintable*100/len(b) > 30
}
