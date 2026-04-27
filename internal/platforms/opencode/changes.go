package opencode

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// maxSnapshotLen caps the length of a per-file before/after snapshot
// returned in the changes payload. Mirrors the cap used by the live
// session converter (truncatePartOutput) so a single response can't
// blow up memory on the client. Files that exceed this length get
// truncated with a sentinel suffix; the diff renderer copes by
// showing the truncated portion as-is.
const maxSnapshotLen = 200_000

// truncationSuffix is appended to before/after snapshots that exceed
// maxSnapshotLen so the frontend can detect (and label) the cut.
const truncationSuffix = "\n... (truncated)"

// editToolNames is the set of tool names whose parts represent
// file-touching operations. We keep this small and explicit instead
// of a prefix match: parts whose tool isn't in this list (Read, Bash,
// Grep, ...) are not aggregated.
var editToolNames = map[string]struct{}{
	"edit":      {},
	"write":     {},
	"mcp_edit":  {},
	"mcp_write": {},
	"mcp_Write": {},
	"mcp_Edit":  {},
}

// aggregateChanges walks a session's parts and produces the per-file
// changes summary. Parts are assumed to be ordered by timeCreated
// ascending (db.GetSessionParts returns them in insertion order, which
// matches creation time). The directory argument is used to compute
// FileChange.DisplayPath relative to the session's working directory.
//
// Algorithm:
//  1. Filter parts to those with type=="tool" and a tool name in
//     editToolNames.
//  2. For each match, extract path/before/after/additions/deletions.
//     The Edit tool has state.metadata.filediff.{before,after,
//     additions,deletions}; when missing (older OpenCode versions,
//     MCP variants), fall back to counting lines from
//     state.input.{oldString,newString} or state.input.content.
//  3. Group by path. The first edit's before-snapshot becomes the
//     file's Before; the last edit's after-snapshot becomes its After.
//     Counts are summed.
//
// Returns Supported=true with a possibly-empty Files slice. The caller
// is expected to set the SessionID on the returned struct (or rely on
// the value here).
func aggregateChanges(sessionID, sessionDir string, parts []db.Part) *platforms.SessionChanges {
	out := &platforms.SessionChanges{
		SessionID: sessionID,
		Supported: true,
		Files:     []platforms.FileChange{},
	}
	// Insertion-ordered map: a slice of pointers indexed by a parallel
	// path->index map. Order matters because the first-seen path
	// becomes the first emitted file, which keeps the API stable for
	// snapshot-style tests.
	byPath := map[string]*platforms.FileChange{}
	order := []string{}

	for _, p := range parts {
		edit, ok := parseEditPart(p)
		if !ok {
			continue
		}

		fc, exists := byPath[edit.path]
		if !exists {
			fc = &platforms.FileChange{
				Path:        edit.path,
				DisplayPath: relativise(edit.path, sessionDir),
				FirstEditAt: edit.timeCreated,
				Before:      edit.before,
				Edits:       []platforms.Edit{},
			}
			byPath[edit.path] = fc
			order = append(order, edit.path)
		}

		fc.LastEditAt = edit.timeCreated
		fc.After = edit.after
		fc.Additions += edit.additions
		fc.Deletions += edit.deletions
		fc.EditCount++
		fc.Edits = append(fc.Edits, platforms.Edit{
			PartID:      edit.partID,
			MessageID:   edit.messageID,
			TimeCreated: edit.timeCreated,
			Tool:        edit.tool,
			Additions:   edit.additions,
			Deletions:   edit.deletions,
			Before:      edit.before,
			After:       edit.after,
		})
	}

	for _, path := range order {
		fc := byPath[path]
		// Skip files whose net effect is zero. Three checks, each
		// matching a distinct cause of a pointless "(no changes)"
		// render in the UI:
		//   1. No additions or deletions ever counted (e.g. an
		//      edit tool fired against a file but the filediff
		//      came back empty).
		//   2. First-before exactly matches last-after — a file
		//      that was edited then reverted to its original
		//      content.
		//   3. line-equivalent before/after — strings that differ
		//      only in trailing whitespace or line endings. The
		//      frontend's line-based diff renders zero rows for
		//      these, so we filter on the same boundary the
		//      renderer uses.
		if fc.Additions == 0 && fc.Deletions == 0 {
			continue
		}
		if fc.Before == fc.After {
			continue
		}
		if linesEqual(fc.Before, fc.After) {
			continue
		}
		fc.Before = truncateSnapshot(fc.Before)
		fc.After = truncateSnapshot(fc.After)
		for i := range fc.Edits {
			fc.Edits[i].Before = truncateSnapshot(fc.Edits[i].Before)
			fc.Edits[i].After = truncateSnapshot(fc.Edits[i].After)
		}
		out.Files = append(out.Files, *fc)
		// Totals reflect what we ship. Files filtered out above
		// (net-zero / reverted) don't contribute, so the header
		// "+A -D" matches the displayed file groups.
		out.TotalAdditions += fc.Additions
		out.TotalDeletions += fc.Deletions
	}
	out.FilesChanged = len(out.Files)
	return out
}

// editInfo is the parsed shape of one edit/write tool part. Internal
// to this file; the public aggregation produces platforms.Edit.
type editInfo struct {
	partID      string
	messageID   string
	timeCreated int64
	tool        string
	path        string
	before      string
	after       string
	additions   int
	deletions   int
}

// parseEditPart inspects one Part and returns an editInfo if it
// represents a file-touching tool call we want to aggregate. The bool
// is false for non-tool parts, non-edit tools, or malformed payloads.
func parseEditPart(p db.Part) (editInfo, bool) {
	var raw struct {
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Input    map[string]json.RawMessage `json:"input"`
			Metadata struct {
				Filediff *struct {
					File      string `json:"file"`
					Before    string `json:"before"`
					After     string `json:"after"`
					Additions int    `json:"additions"`
					Deletions int    `json:"deletions"`
				} `json:"filediff"`
			} `json:"metadata"`
		} `json:"state"`
	}
	if err := json.Unmarshal(p.Data, &raw); err != nil {
		return editInfo{}, false
	}
	if raw.Type != "tool" {
		return editInfo{}, false
	}
	if _, ok := editToolNames[raw.Tool]; !ok {
		return editInfo{}, false
	}

	info := editInfo{
		partID:      p.ID,
		messageID:   p.MessageID,
		timeCreated: p.TimeCreated,
		tool:        raw.Tool,
	}

	// Path: prefer filediff.file, fall back to state.input.filePath /
	// file_path / path. Tool variants are inconsistent so check all.
	if raw.State.Metadata.Filediff != nil && raw.State.Metadata.Filediff.File != "" {
		info.path = raw.State.Metadata.Filediff.File
	} else {
		info.path = firstStringField(raw.State.Input, "filePath", "file_path", "path")
	}
	if info.path == "" {
		return editInfo{}, false
	}

	// Counts + snapshots from filediff when available — that's the
	// authoritative source.
	if fd := raw.State.Metadata.Filediff; fd != nil {
		info.before = fd.Before
		info.after = fd.After
		info.additions = fd.Additions
		info.deletions = fd.Deletions
		return info, true
	}

	// Fallback path: derive from input. For Write, the new file is
	// state.input.content with no before; counted as all-additions.
	// For Edit, count line-diff between oldString and newString.
	switch raw.Tool {
	case "write", "mcp_write", "mcp_Write":
		content := firstStringField(raw.State.Input, "content")
		info.after = content
		info.additions = countLines(content)
	default:
		oldStr := firstStringField(raw.State.Input, "oldString", "old_string")
		newStr := firstStringField(raw.State.Input, "newString", "new_string")
		info.before = oldStr
		info.after = newStr
		// Naive count: every old line is a deletion, every new line
		// is an addition. Loses precision compared to OpenCode's
		// filediff (which counts only changed lines), but only used
		// when filediff is missing.
		if oldStr != "" {
			info.deletions = countLines(oldStr)
		}
		if newStr != "" {
			info.additions = countLines(newStr)
		}
	}
	return info, true
}

// firstStringField returns the first non-empty string value found in m
// for any of the given keys. Used to handle camelCase/snake_case key
// variants without hard-coding two reads at every call site. Named
// distinctly from operations.stringField (single-key, interface{}-valued)
// to avoid shadowing.
func firstStringField(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

// countLines counts \n-delimited lines in s. An empty string is zero
// lines; "foo" is one line; "foo\nbar" is two; a trailing newline
// doesn't add a line ("foo\n" is one).
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// relativise tries to express path relative to base, falling back to
// path when that fails or escapes upwards. Returns path unchanged when
// base is empty.
func relativise(path, base string) string {
	if base == "" {
		return path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	// Anything that escapes the session directory is shown as the
	// original absolute path — relative paths that start with ".."
	// are confusing in a sidebar.
	if strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// truncateSnapshot caps a string at maxSnapshotLen, appending the
// truncation sentinel when applied. Empty input passes through.
func truncateSnapshot(s string) string {
	if len(s) <= maxSnapshotLen {
		return s
	}
	return s[:maxSnapshotLen] + truncationSuffix
}

// linesEqual reports whether two strings are line-by-line equivalent.
// Mirrors the frontend's line-based diff: both sides are split on '\n'
// and compared element-wise, so differences that are only in trailing
// whitespace at end-of-string, mixed CRLF/LF line endings, or a missing
// final newline don't count as a change.
//
// We split on '\n' and trim a trailing '\r' from each side before
// comparing, matching what `simpleDiff` would consider equal lines.
func linesEqual(a, b string) bool {
	if a == b {
		return true
	}
	as := strings.Split(a, "\n")
	bs := strings.Split(b, "\n")
	// Drop a single empty trailing element produced when the input
	// ends with '\n'. This makes "foo\n" and "foo" line-equivalent.
	if n := len(as); n > 0 && as[n-1] == "" {
		as = as[:n-1]
	}
	if n := len(bs); n > 0 && bs[n-1] == "" {
		bs = bs[:n-1]
	}
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if strings.TrimRight(as[i], "\r") != strings.TrimRight(bs[i], "\r") {
			return false
		}
	}
	return true
}
