package opencode

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// maxSnapshotLen caps the length of a per-file before/after snapshot
// or concatenated patch returned in the changes payload. Mirrors the
// cap used by the live session converter (truncatePartOutput) so a
// single response can't blow up memory on the client. Files that
// exceed this length get truncated with a sentinel suffix; the diff
// renderer copes by showing the truncated portion as-is.
const maxSnapshotLen = 200_000

// truncationSuffix is appended to before/after snapshots that exceed
// maxSnapshotLen so the frontend can detect (and label) the cut.
const truncationSuffix = "\n... (truncated)"

// editToolNames is the set of tool names whose parts represent
// file-touching operations. We keep this small and explicit instead
// of a prefix match: parts whose tool isn't in this list (Read, Bash,
// Grep, ...) are not aggregated. apply_patch is special-cased in
// parseApplyPatchPart because it carries one entry per file in
// state.metadata.files[]; for the remaining tools each part contributes
// exactly one editInfo.
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
//     editToolNames or "apply_patch". The latter contributes one
//     editInfo per entry in state.metadata.files[].
//  2. Each editInfo carries the per-edit counts from filediff
//     (modern OpenCode emits them in `state.metadata.filediff` for
//     edit/write or `state.metadata.files[]` for apply_patch). Older
//     parts that still carry before/after snapshots are accepted and
//     translated.
//  3. Group by path. Counts are summed; per-edit patches are
//     concatenated into a single per-file patch. Legacy snapshots
//     (when present) become FileChange.Before / FileChange.After so
//     the frontend's pre-patch fallback keeps rendering them.
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
		for _, edit := range parseEditsFromPart(p) {
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
			// Preserve the latest legacy after-snapshot so the
			// pre-patch frontend renderer keeps working for older
			// sessions. New-schema edits leave it empty.
			if edit.after != "" {
				fc.After = edit.after
			}
			fc.Additions += edit.additions
			fc.Deletions += edit.deletions
			fc.EditCount++
			if edit.patch != "" {
				if fc.Patch == "" {
					fc.Patch = edit.patch
				} else {
					// Separate concatenated patches with a blank line
					// so consecutive `Index:` headers parse as
					// independent files / hunks downstream.
					fc.Patch = fc.Patch + "\n" + edit.patch
				}
			}
			fc.Edits = append(fc.Edits, platforms.Edit{
				PartID:      edit.partID,
				MessageID:   edit.messageID,
				TimeCreated: edit.timeCreated,
				Tool:        edit.tool,
				Additions:   edit.additions,
				Deletions:   edit.deletions,
				Patch:       edit.patch,
				Before:      edit.before,
				After:       edit.after,
			})
		}
	}

	for _, path := range order {
		fc := byPath[path]
		// Drop a file iff every edit reported zero additions and
		// zero deletions. We trust OpenCode's per-edit counts as
		// the single source of truth — no second-guessing on
		// whitespace, CRLF, or "net effect". Sessions where the
		// agent did real work but ended up reverting it still
		// surface so the user can see the tokens they spent.
		if fc.Additions == 0 && fc.Deletions == 0 {
			continue
		}
		fc.Before = truncateSnapshot(fc.Before)
		fc.After = truncateSnapshot(fc.After)
		fc.Patch = truncateSnapshot(fc.Patch)
		for i := range fc.Edits {
			fc.Edits[i].Before = truncateSnapshot(fc.Edits[i].Before)
			fc.Edits[i].After = truncateSnapshot(fc.Edits[i].After)
			fc.Edits[i].Patch = truncateSnapshot(fc.Edits[i].Patch)
		}
		out.Files = append(out.Files, *fc)
		out.TotalAdditions += fc.Additions
		out.TotalDeletions += fc.Deletions
	}
	out.FilesChanged = len(out.Files)
	return out
}

// editInfo is the parsed shape of one edit/write/apply_patch entry.
// Internal to this file; the public aggregation produces
// platforms.Edit. apply_patch produces multiple editInfos per part —
// one per file in state.metadata.files[].
type editInfo struct {
	partID      string
	messageID   string
	timeCreated int64
	tool        string
	path        string
	patch       string // unified-diff body when the new schema is used
	before      string // legacy snapshot, empty for patch-style parts
	after       string // legacy snapshot, empty for patch-style parts
	additions   int
	deletions   int
}

// parseEditsFromPart inspects one Part and returns zero or more
// editInfo records. Most tools yield 0 or 1 record; apply_patch can
// yield several (one per file in metadata.files[]).
func parseEditsFromPart(p db.Part) []editInfo {
	var head struct {
		Type string `json:"type"`
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(p.Data, &head); err != nil {
		return nil
	}
	if head.Type != "tool" {
		return nil
	}
	if head.Tool == "apply_patch" {
		return parseApplyPatchPart(p)
	}
	if _, ok := editToolNames[head.Tool]; !ok {
		return nil
	}
	if e, ok := parseEditPart(p); ok {
		return []editInfo{e}
	}
	return nil
}

// parseEditPart inspects one edit/write/mcp_edit/mcp_write Part and
// returns an editInfo if it represents a file-touching tool call we
// want to aggregate. The bool is false for malformed payloads or when
// no usable file path can be extracted.
//
// Supports both schemas in the wild:
//   - Legacy: state.metadata.filediff = {file, before, after,
//     additions, deletions}.
//   - Modern: state.metadata.filediff = {file, patch, additions,
//     deletions} with no before/after pair. The Write tool dropped
//     filediff entirely; we synthesise an all-additions patch from
//     state.input.content.
func parseEditPart(p db.Part) (editInfo, bool) {
	var raw struct {
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Input    map[string]json.RawMessage `json:"input"`
			Metadata struct {
				Filediff *struct {
					File      string `json:"file"`
					Patch     string `json:"patch"`
					Before    string `json:"before"`
					After     string `json:"after"`
					Additions int    `json:"additions"`
					Deletions int    `json:"deletions"`
				} `json:"filediff"`
				Diff     string `json:"diff"`
				Filepath string `json:"filepath"`
				Exists   bool   `json:"exists"`
			} `json:"metadata"`
		} `json:"state"`
	}
	if err := json.Unmarshal(p.Data, &raw); err != nil {
		return editInfo{}, false
	}

	info := editInfo{
		partID:      p.ID,
		messageID:   p.MessageID,
		timeCreated: p.TimeCreated,
		tool:        raw.Tool,
	}

	// Path: prefer filediff.file, fall back to metadata.filepath
	// (modern Write), then state.input.{filePath,file_path,path}.
	switch {
	case raw.State.Metadata.Filediff != nil && raw.State.Metadata.Filediff.File != "":
		info.path = raw.State.Metadata.Filediff.File
	case raw.State.Metadata.Filepath != "":
		info.path = raw.State.Metadata.Filepath
	default:
		info.path = firstStringField(raw.State.Input, "filePath", "file_path", "path")
	}
	if info.path == "" {
		return editInfo{}, false
	}

	// Modern + legacy edit schema both populate filediff. Counts are
	// authoritative; patch and before/after are best-effort and may
	// each be empty depending on the OpenCode version.
	if fd := raw.State.Metadata.Filediff; fd != nil {
		info.before = fd.Before
		info.after = fd.After
		info.additions = fd.Additions
		info.deletions = fd.Deletions
		info.patch = fd.Patch
		// Some older OpenCode versions emit filediff *without* a
		// patch field but DO emit metadata.diff alongside as the
		// unified-diff text. Use it as a fallback so the frontend
		// always has something to render.
		if info.patch == "" && raw.State.Metadata.Diff != "" {
			info.patch = raw.State.Metadata.Diff
		}
		return info, true
	}

	// No filediff at all. This is the modern Write tool: only
	// state.input.content is populated. Synthesise an all-additions
	// patch so the sidebar can render something. Counts come from
	// the line count of the new content. We don't bother for Edit
	// without filediff (older MCP variants) — fall back to the
	// oldString/newString line-count estimate.
	switch raw.Tool {
	case "write", "mcp_write", "mcp_Write":
		content := firstStringField(raw.State.Input, "content")
		info.after = content
		info.additions = countLines(content)
		info.patch = synthesiseAllAdditionsPatch(info.path, content)
	default:
		oldStr := firstStringField(raw.State.Input, "oldString", "old_string")
		newStr := firstStringField(raw.State.Input, "newString", "new_string")
		info.before = oldStr
		info.after = newStr
		if oldStr != "" {
			info.deletions = countLines(oldStr)
		}
		if newStr != "" {
			info.additions = countLines(newStr)
		}
	}
	return info, true
}

// parseApplyPatchPart explodes an apply_patch tool part into one
// editInfo per touched file. The new schema groups every file in
// state.metadata.files[]: each entry has its own filePath, patch,
// additions and deletions.
func parseApplyPatchPart(p db.Part) []editInfo {
	var raw struct {
		State struct {
			Metadata struct {
				Files []struct {
					FilePath     string `json:"filePath"`
					RelativePath string `json:"relativePath"`
					Type         string `json:"type"`
					Patch        string `json:"patch"`
					Additions    int    `json:"additions"`
					Deletions    int    `json:"deletions"`
				} `json:"files"`
			} `json:"metadata"`
		} `json:"state"`
	}
	if err := json.Unmarshal(p.Data, &raw); err != nil {
		return nil
	}
	if len(raw.State.Metadata.Files) == 0 {
		return nil
	}
	out := make([]editInfo, 0, len(raw.State.Metadata.Files))
	for _, f := range raw.State.Metadata.Files {
		if f.FilePath == "" {
			continue
		}
		out = append(out, editInfo{
			partID:      p.ID,
			messageID:   p.MessageID,
			timeCreated: p.TimeCreated,
			tool:        "apply_patch",
			path:        f.FilePath,
			patch:       f.Patch,
			additions:   f.Additions,
			deletions:   f.Deletions,
		})
	}
	return out
}

// synthesiseAllAdditionsPatch builds a unified-diff body that
// represents the entire file as additions. Used for Write parts where
// OpenCode no longer emits a filediff: we know the new content but
// have no original to diff against, so the closest representation is
// an all-green patch. The format mimics what `git diff` produces for a
// new file so RawDiffView's parser handles it without changes.
func synthesiseAllAdditionsPatch(path, content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	// strings.Split leaves a trailing empty element when the input
	// ends with '\n'; strip it so we don't render a phantom blank
	// line after the file content.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var b strings.Builder
	b.WriteString("Index: ")
	b.WriteString(path)
	b.WriteString("\n===================================================================\n")
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteString("\n+++ ")
	b.WriteString(path)
	b.WriteString("\n@@ -0,0 +1,")
	b.WriteString(itoa(len(lines)))
	b.WriteString(" @@\n")
	for _, line := range lines {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// itoa is a tiny strconv.Itoa stand-in to keep this file's import
// list minimal. Inlining the conversion avoids a single-call import
// of strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
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
