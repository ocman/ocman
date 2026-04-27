package opencode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// makePart constructs a db.Part with a JSON-encoded data payload.
// The helper keeps tests focused on the part *shape* rather than the
// JSON plumbing.
func makePart(t *testing.T, id, messageID string, timeCreated int64, data map[string]any) db.Part {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal part data: %v", err)
	}
	return db.Part{
		ID:          id,
		MessageID:   messageID,
		SessionID:   "ses_test",
		TimeCreated: timeCreated,
		Data:        raw,
	}
}

// editPart returns a part that mimics OpenCode's Edit tool with
// state.metadata.filediff populated.
func editPart(t *testing.T, id string, ts int64, file, before, after string, adds, dels int) db.Part {
	t.Helper()
	return makePart(t, id, "msg_"+id, ts, map[string]any{
		"type": "tool",
		"tool": "edit",
		"state": map[string]any{
			"input": map[string]any{
				"filePath":  file,
				"oldString": before,
				"newString": after,
			},
			"metadata": map[string]any{
				"filediff": map[string]any{
					"file":      file,
					"before":    before,
					"after":     after,
					"additions": adds,
					"deletions": dels,
				},
			},
		},
	})
}

func TestAggregateChanges_SingleEdit(t *testing.T) {
	parts := []db.Part{
		editPart(t, "p1", 1000, "/work/src/hero.tsx",
			"old line\n", "new line\nnew line 2\n", 2, 1),
	}
	got := aggregateChanges("ses_test", "/work", parts)
	if !got.Supported {
		t.Fatalf("Supported should be true")
	}
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
	if got.TotalAdditions != 2 || got.TotalDeletions != 1 {
		t.Fatalf("totals = %d/%d, want 2/1", got.TotalAdditions, got.TotalDeletions)
	}
	f := got.Files[0]
	if f.DisplayPath != "src/hero.tsx" {
		t.Errorf("DisplayPath = %q, want src/hero.tsx", f.DisplayPath)
	}
	if f.EditCount != 1 || len(f.Edits) != 1 {
		t.Errorf("EditCount = %d, len(Edits) = %d", f.EditCount, len(f.Edits))
	}
	if f.Before != "old line\n" || f.After != "new line\nnew line 2\n" {
		t.Errorf("Before/After mismatch: %q -> %q", f.Before, f.After)
	}
}

func TestAggregateChanges_MultipleEditsSameFile(t *testing.T) {
	parts := []db.Part{
		editPart(t, "p1", 100, "/work/a.go", "v0", "v1", 1, 1),
		editPart(t, "p2", 200, "/work/a.go", "v1", "v2", 2, 1),
		editPart(t, "p3", 300, "/work/a.go", "v2", "v3", 1, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
	f := got.Files[0]
	// Sums:
	if f.Additions != 4 || f.Deletions != 2 {
		t.Errorf("counts = %d/%d, want 4/2", f.Additions, f.Deletions)
	}
	// First-before / last-after:
	if f.Before != "v0" {
		t.Errorf("Before = %q, want v0 (first edit's before)", f.Before)
	}
	if f.After != "v3" {
		t.Errorf("After = %q, want v3 (last edit's after)", f.After)
	}
	if f.FirstEditAt != 100 || f.LastEditAt != 300 {
		t.Errorf("times = %d/%d, want 100/300", f.FirstEditAt, f.LastEditAt)
	}
	if f.EditCount != 3 || len(f.Edits) != 3 {
		t.Errorf("EditCount = %d", f.EditCount)
	}
}

func TestAggregateChanges_WriteTool_NoBefore(t *testing.T) {
	// Write tool: no filediff metadata, just state.input.content.
	parts := []db.Part{
		makePart(t, "p1", "m1", 1, map[string]any{
			"type": "tool",
			"tool": "write",
			"state": map[string]any{
				"input": map[string]any{
					"filePath": "/work/new.txt",
					"content":  "line1\nline2\nline3",
				},
			},
		}),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d", got.FilesChanged)
	}
	f := got.Files[0]
	if f.Before != "" {
		t.Errorf("Before should be empty for Write, got %q", f.Before)
	}
	if f.After != "line1\nline2\nline3" {
		t.Errorf("After = %q", f.After)
	}
	if f.Additions != 3 || f.Deletions != 0 {
		t.Errorf("counts = %d/%d, want 3/0", f.Additions, f.Deletions)
	}
}

func TestAggregateChanges_MCPTools(t *testing.T) {
	parts := []db.Part{
		makePart(t, "p1", "m1", 1, map[string]any{
			"type": "tool",
			"tool": "mcp_edit",
			"state": map[string]any{
				"input": map[string]any{
					"filePath":  "/work/mcp.go",
					"oldString": "a\n",
					"newString": "b\nc\n",
				},
				"metadata": map[string]any{
					"filediff": map[string]any{
						"file":      "/work/mcp.go",
						"before":    "a\n",
						"after":     "b\nc\n",
						"additions": 2,
						"deletions": 1,
					},
				},
			},
		}),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("MCP edit tool not aggregated")
	}
	if got.Files[0].Edits[0].Tool != "mcp_edit" {
		t.Errorf("Tool = %q, want mcp_edit", got.Files[0].Edits[0].Tool)
	}
}

func TestAggregateChanges_FallbackWithoutFilediff(t *testing.T) {
	// Older OpenCode versions / some MCP servers don't emit
	// state.metadata.filediff. We must still aggregate using
	// state.input.oldString/newString and a naive line count.
	parts := []db.Part{
		makePart(t, "p1", "m1", 1, map[string]any{
			"type": "tool",
			"tool": "edit",
			"state": map[string]any{
				"input": map[string]any{
					"filePath":  "/work/legacy.go",
					"oldString": "one\ntwo",
					"newString": "ONE\nTWO\nTHREE",
				},
				// no metadata.filediff
			},
		}),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d", got.FilesChanged)
	}
	f := got.Files[0]
	if f.Additions != 3 || f.Deletions != 2 {
		t.Errorf("fallback counts = %d/%d, want 3/2", f.Additions, f.Deletions)
	}
	if f.Before != "one\ntwo" || f.After != "ONE\nTWO\nTHREE" {
		t.Errorf("snapshots not pulled from input: %q -> %q", f.Before, f.After)
	}
}

func TestAggregateChanges_NoEdits(t *testing.T) {
	parts := []db.Part{
		makePart(t, "p1", "m1", 1, map[string]any{
			"type": "text", "text": "hi",
		}),
		makePart(t, "p2", "m1", 2, map[string]any{
			"type": "tool", "tool": "read",
			"state": map[string]any{"input": map[string]any{"filePath": "/x"}},
		}),
	}
	got := aggregateChanges("ses", "/work", parts)
	if !got.Supported {
		t.Errorf("Supported should be true")
	}
	if got.FilesChanged != 0 {
		t.Errorf("FilesChanged = %d, want 0", got.FilesChanged)
	}
	if got.Files == nil {
		t.Errorf("Files should be empty slice, not nil (so JSON serialises as [])")
	}
}

func TestAggregateChanges_MultipleFiles_Ordered(t *testing.T) {
	// Files appear in first-seen order.
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/b.go", "x", "y", 1, 1),
		editPart(t, "p2", 2, "/work/a.go", "x", "y", 1, 1),
		editPart(t, "p3", 3, "/work/b.go", "y", "z", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 2 {
		t.Fatalf("FilesChanged = %d", got.FilesChanged)
	}
	if got.Files[0].DisplayPath != "b.go" {
		t.Errorf("first file = %q, want b.go (first-seen order)",
			got.Files[0].DisplayPath)
	}
	if got.Files[1].DisplayPath != "a.go" {
		t.Errorf("second file = %q, want a.go", got.Files[1].DisplayPath)
	}
}

func TestAggregateChanges_DisplayPathOutsideSessionDir(t *testing.T) {
	parts := []db.Part{
		editPart(t, "p1", 1, "/elsewhere/foo.go", "a", "b", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	// Path escapes /work; should fall back to absolute path so the
	// user isn't shown a confusing "../elsewhere/foo.go".
	if got.Files[0].DisplayPath != "/elsewhere/foo.go" {
		t.Errorf("DisplayPath = %q, want absolute fallback",
			got.Files[0].DisplayPath)
	}
}

func TestAggregateChanges_EmptySessionDir(t *testing.T) {
	parts := []db.Part{
		editPart(t, "p1", 1, "/abs/path/foo.go", "a", "b", 1, 1),
	}
	got := aggregateChanges("ses", "", parts)
	if got.Files[0].DisplayPath != "/abs/path/foo.go" {
		t.Errorf("DisplayPath = %q, want unchanged", got.Files[0].DisplayPath)
	}
}

func TestAggregateChanges_TruncatesLargeSnapshots(t *testing.T) {
	huge := strings.Repeat("a", maxSnapshotLen+100)
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/big.txt", "", huge, 1, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	f := got.Files[0]
	if !strings.HasSuffix(f.After, truncationSuffix) {
		t.Errorf("After should end with truncation suffix")
	}
	if len(f.After) > maxSnapshotLen+len(truncationSuffix) {
		t.Errorf("After length = %d, expected ~%d", len(f.After), maxSnapshotLen)
	}
}

func TestAggregateChanges_NonToolPartsIgnored(t *testing.T) {
	parts := []db.Part{
		makePart(t, "p1", "m1", 1, map[string]any{
			"type": "tool", "tool": "bash",
			"state": map[string]any{"input": map[string]any{"command": "ls"}},
		}),
		makePart(t, "p2", "m1", 2, map[string]any{
			"type": "step-start",
		}),
		editPart(t, "p3", 3, "/work/x.go", "a", "b", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
}

func TestAggregateChanges_MalformedPartSkipped(t *testing.T) {
	parts := []db.Part{
		{ID: "bad", MessageID: "m", TimeCreated: 1, Data: []byte("not json")},
		editPart(t, "p1", 2, "/work/x.go", "a", "b", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1 (malformed skipped)", got.FilesChanged)
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"foo", 1},
		{"foo\n", 1},
		{"foo\nbar", 2},
		{"foo\nbar\n", 2},
		{"\n", 1}, // single empty line
		{"a\nb\nc\n", 3},
	}
	for _, tc := range cases {
		got := countLines(tc.in)
		if got != tc.want {
			t.Errorf("countLines(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestLinesEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "foo\nbar", "foo\nbar", true},
		{"empty both", "", "", true},
		{"trailing newline only on a", "foo\nbar\n", "foo\nbar", true},
		{"trailing newline only on b", "foo\nbar", "foo\nbar\n", true},
		{"crlf vs lf", "foo\r\nbar\r\n", "foo\nbar\n", true},
		{"different content", "foo", "bar", false},
		{"different line count", "foo\nbar", "foo", false},
		{"trailing whitespace mid-line", "foo \nbar", "foo\nbar", false},
	}
	for _, tc := range cases {
		got := linesEqual(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("%s: linesEqual(%q, %q) = %v, want %v",
				tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAggregateChanges_FiltersRevertedFile(t *testing.T) {
	// File goes A -> B -> A. Per-edit counts are non-zero (each
	// edit did something), but the net effect is no change. The
	// aggregator should drop the file entirely so the UI doesn't
	// show a "(no changes)" group.
	parts := []db.Part{
		editPart(t, "p1", 100, "/work/x.go", "A\n", "B\n", 1, 1),
		editPart(t, "p2", 200, "/work/x.go", "B\n", "A\n", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 0 {
		t.Errorf("FilesChanged = %d, want 0 (reverted file should be filtered)", got.FilesChanged)
	}
	if got.TotalAdditions != 0 || got.TotalDeletions != 0 {
		t.Errorf("totals = %d/%d, want 0/0 after filtering",
			got.TotalAdditions, got.TotalDeletions)
	}
}

func TestAggregateChanges_FiltersTrailingWhitespaceOnlyChange(t *testing.T) {
	// Edit that only adds a trailing newline. The byte content
	// differs but the line-by-line view is unchanged, so the
	// frontend would render zero diff rows. Filter at the
	// boundary the renderer uses.
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/y.go", "hello", "hello\n", 0, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 0 {
		t.Errorf("FilesChanged = %d, want 0 (trailing-newline-only edit filtered)",
			got.FilesChanged)
	}
}

func TestAggregateChanges_FiltersCRLFOnlyChange(t *testing.T) {
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/z.go", "a\r\nb\r\n", "a\nb\n", 0, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 0 {
		t.Errorf("FilesChanged = %d, want 0 (CRLF/LF-only difference filtered)",
			got.FilesChanged)
	}
}

func TestAggregateChanges_KeepsFileWithRealChange(t *testing.T) {
	// Sanity: trailing whitespace MID-LINE is a real difference
	// (it shows up as a changed character), so we must keep it.
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/k.go", "foo\nbar", "foo \nbar", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Errorf("FilesChanged = %d, want 1 (mid-line whitespace IS a real change)",
			got.FilesChanged)
	}
}
