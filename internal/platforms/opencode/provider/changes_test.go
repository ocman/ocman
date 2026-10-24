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

func TestAggregateChanges_KeepsRevertedFile(t *testing.T) {
	// File goes A -> B -> A. Per-edit counts are non-zero (each edit
	// did real work). Under the new aggregation rule we trust those
	// counts and surface the file rather than try to compute a "net"
	// effect — OpenCode's own counts are authoritative, and dropping
	// edits the user spent tokens on was actively misleading when
	// the new patch-style schema replaced the before/after snapshots
	// (every file looked "reverted" because Before/After were empty).
	parts := []db.Part{
		editPart(t, "p1", 100, "/work/x.go", "A\n", "B\n", 1, 1),
		editPart(t, "p2", 200, "/work/x.go", "B\n", "A\n", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Errorf("FilesChanged = %d, want 1 (reverted file should still be surfaced)", got.FilesChanged)
	}
	if got.TotalAdditions != 2 || got.TotalDeletions != 2 {
		t.Errorf("totals = %d/%d, want 2/2 (sum of per-edit counts)",
			got.TotalAdditions, got.TotalDeletions)
	}
}

func TestAggregateChanges_FiltersZeroCountEdit(t *testing.T) {
	// An edit that produces zero per-edit additions and deletions
	// (e.g. trailing-newline noise reported as 0/0 by OpenCode) is
	// dropped. We rely on the upstream counts as the single source
	// of truth — no extra heuristic on whitespace/CRLF.
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/y.go", "hello", "hello\n", 0, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 0 {
		t.Errorf("FilesChanged = %d, want 0 (zero-count edit filtered)", got.FilesChanged)
	}
}

func TestAggregateChanges_KeepsFileWithRealChange(t *testing.T) {
	// Sanity: any edit with non-zero per-edit counts is kept. The
	// aggregator no longer second-guesses OpenCode's accounting.
	parts := []db.Part{
		editPart(t, "p1", 1, "/work/k.go", "foo\nbar", "foo \nbar", 1, 1),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Errorf("FilesChanged = %d, want 1", got.FilesChanged)
	}
}

// patchFiledidffPart returns a part shaped like the new (patch-style)
// OpenCode schema: state.metadata.filediff carries `{file, patch,
// additions, deletions}` with no before/after snapshots. This is what
// modern OpenCode versions emit.
func patchFilediffPart(t *testing.T, id string, ts int64, file, patch string, adds, dels int) db.Part {
	t.Helper()
	return makePart(t, id, "msg_"+id, ts, map[string]any{
		"type": "tool",
		"tool": "edit",
		"state": map[string]any{
			"input": map[string]any{
				"filePath":  file,
				"oldString": "irrelevant — modern parts carry the diff in metadata",
				"newString": "irrelevant",
			},
			"metadata": map[string]any{
				"filediff": map[string]any{
					"file":      file,
					"patch":     patch,
					"additions": adds,
					"deletions": dels,
				},
			},
		},
	})
}

func TestAggregateChanges_NewSchemaPatchOnly(t *testing.T) {
	// New OpenCode schema: filediff has a `patch` (unified diff)
	// rather than before/after snapshots. The aggregator must read
	// counts from filediff.{additions,deletions} and surface the
	// patch through FileChange.Patch / Edit.Patch.
	patch := "@@ -1,2 +1,3 @@\n line\n+added\n line2\n"
	parts := []db.Part{
		patchFilediffPart(t, "p1", 100, "/work/foo.go", patch, 1, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
	f := got.Files[0]
	if f.Additions != 1 || f.Deletions != 0 {
		t.Errorf("counts = %d/%d, want 1/0", f.Additions, f.Deletions)
	}
	if f.Patch == "" {
		t.Errorf("FileChange.Patch should be populated for new-schema parts")
	}
	if !strings.Contains(f.Patch, "+added") {
		t.Errorf("FileChange.Patch missing expected hunk: %q", f.Patch)
	}
	if len(f.Edits) != 1 || f.Edits[0].Patch == "" {
		t.Errorf("per-edit Patch should be populated")
	}
	// Legacy snapshots should be empty when only a patch is available.
	if f.Before != "" || f.After != "" {
		t.Errorf("Before/After should be empty for new-schema parts, got %q/%q", f.Before, f.After)
	}
}

func TestAggregateChanges_NewSchemaMultipleEditsConcatPatch(t *testing.T) {
	// Two patch-style edits on the same file. The per-file Patch is
	// the concatenation of every edit's patch, in chronological
	// order, separated by a single blank line so a multi-edit view
	// still parses as a sequence of unified-diff hunks.
	p1 := "@@ -1,1 +1,2 @@\n a\n+b\n"
	p2 := "@@ -2,1 +2,2 @@\n b\n+c\n"
	parts := []db.Part{
		patchFilediffPart(t, "p1", 100, "/work/x.go", p1, 1, 0),
		patchFilediffPart(t, "p2", 200, "/work/x.go", p2, 1, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d", got.FilesChanged)
	}
	f := got.Files[0]
	if f.EditCount != 2 || len(f.Edits) != 2 {
		t.Errorf("EditCount/Edits = %d/%d, want 2/2", f.EditCount, len(f.Edits))
	}
	if f.Additions != 2 || f.Deletions != 0 {
		t.Errorf("counts = %d/%d, want 2/0", f.Additions, f.Deletions)
	}
	if !strings.Contains(f.Patch, "+b") || !strings.Contains(f.Patch, "+c") {
		t.Errorf("concat patch missing hunks: %q", f.Patch)
	}
}

func TestAggregateChanges_WriteToolNoFilediff_NewSchema(t *testing.T) {
	// Modern Write parts have no filediff at all — just
	// state.input.content and state.metadata.{filepath,exists}.
	// The aggregator synthesises an all-additions patch from the
	// content so the sidebar can render it.
	parts := []db.Part{
		makePart(t, "p1", "m1", 1, map[string]any{
			"type": "tool",
			"tool": "write",
			"state": map[string]any{
				"input": map[string]any{
					"filePath": "/work/new.txt",
					"content":  "line1\nline2\nline3",
				},
				"metadata": map[string]any{
					"filepath": "/work/new.txt",
					"exists":   false,
				},
			},
		}),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
	f := got.Files[0]
	if f.Additions != 3 || f.Deletions != 0 {
		t.Errorf("counts = %d/%d, want 3/0", f.Additions, f.Deletions)
	}
	if f.Patch == "" {
		t.Errorf("Write tool should produce a synthetic Patch")
	}
	// Synthetic patch should contain every line as an addition.
	for _, line := range []string{"+line1", "+line2", "+line3"} {
		if !strings.Contains(f.Patch, line) {
			t.Errorf("synthetic patch missing %q: got %q", line, f.Patch)
		}
	}
}

func TestAggregateChanges_ApplyPatchTool_MultiFile(t *testing.T) {
	// apply_patch is a newer tool that ships per-file metadata in
	// state.metadata.files[]: each entry has its own filePath, patch,
	// additions and deletions. One part can therefore touch several
	// files; the aggregator must emit one editInfo per file.
	parts := []db.Part{
		makePart(t, "p1", "m1", 100, map[string]any{
			"type": "tool",
			"tool": "apply_patch",
			"state": map[string]any{
				"input": map[string]any{
					"patchText": "*** Begin Patch\n... (omitted)\n*** End Patch",
				},
				"metadata": map[string]any{
					"files": []any{
						map[string]any{
							"filePath":     "/work/a.go",
							"relativePath": "a.go",
							"type":         "update",
							"patch":        "@@ -1 +1 @@\n-old\n+new\n",
							"additions":    1,
							"deletions":    1,
						},
						map[string]any{
							"filePath":     "/work/b.go",
							"relativePath": "b.go",
							"type":         "update",
							"patch":        "@@ -1 +1,2 @@\n line\n+added\n",
							"additions":    1,
							"deletions":    0,
						},
					},
				},
			},
		}),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 2 {
		t.Fatalf("FilesChanged = %d, want 2", got.FilesChanged)
	}
	if got.TotalAdditions != 2 || got.TotalDeletions != 1 {
		t.Errorf("totals = %d/%d, want 2/1", got.TotalAdditions, got.TotalDeletions)
	}
	// Each file should have its own patch.
	for _, f := range got.Files {
		if f.Patch == "" {
			t.Errorf("file %q missing Patch", f.DisplayPath)
		}
		if f.EditCount != 1 {
			t.Errorf("file %q EditCount = %d, want 1", f.DisplayPath, f.EditCount)
		}
		if len(f.Edits) != 1 || f.Edits[0].Tool != "apply_patch" {
			t.Errorf("file %q tool name not preserved", f.DisplayPath)
		}
	}
}

func TestAggregateChanges_MixedLegacyAndNewSchema(t *testing.T) {
	// A session can contain both old (before/after) and new (patch)
	// parts on the same file when the OpenCode binary upgraded
	// mid-session. The aggregator should accept both, sum counts,
	// and produce a non-empty Patch (concat where available).
	parts := []db.Part{
		editPart(t, "p1", 100, "/work/m.go", "old\n", "new\n", 1, 1),
		patchFilediffPart(t, "p2", 200, "/work/m.go",
			"@@ -1 +1,2 @@\n new\n+added\n", 1, 0),
	}
	got := aggregateChanges("ses", "/work", parts)
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d", got.FilesChanged)
	}
	f := got.Files[0]
	if f.Additions != 2 || f.Deletions != 1 {
		t.Errorf("counts = %d/%d, want 2/1", f.Additions, f.Deletions)
	}
	// The legacy snapshot from p1 should still be available so the
	// frontend's pre-patch fallback path keeps working.
	if f.Before != "old\n" {
		t.Errorf("Before = %q, want legacy snapshot", f.Before)
	}
	if f.Patch == "" {
		t.Errorf("Patch should include the new-schema edit's patch")
	}
}
