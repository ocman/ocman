package platforms

// SessionChanges is the response shape for the session-scoped
// "files changed" aggregation. Adapters that don't support file-change
// aggregation return ErrUnsupported from Platform.SessionChanges; the
// HTTP layer translates that into a Supported=false payload so the
// frontend has a single shape to render.
//
// The aggregation collapses every file-touching tool call (Edit, Write,
// MCP variants) in a session down to one FileChange per file, with
// per-edit detail preserved in FileChange.Edits for UIs that want to
// surface the sequence.
type SessionChanges struct {
	SessionID      string       `json:"sessionId"`
	Supported      bool         `json:"supported"`
	TotalAdditions int          `json:"totalAdditions"`
	TotalDeletions int          `json:"totalDeletions"`
	FilesChanged   int          `json:"filesChanged"`
	Files          []FileChange `json:"files"`
}

// FileChange is the per-file roll-up. The summed Additions/Deletions
// are authoritative — they come from OpenCode's own per-edit
// additions/deletions counts. Patch is the concatenated unified-diff
// body across every edit on the file (newer OpenCode schema). Before
// and After are the legacy first-before / last-after snapshots
// preserved for older parts that still ship them; the frontend
// prefers Patch when present and falls back to a Before/After diff.
type FileChange struct {
	// Path is the file path as captured by the tool call (typically
	// absolute on the user's machine).
	Path string `json:"path"`
	// DisplayPath is Path made relative to the session's working
	// directory when possible, falling back to Path. The frontend
	// uses this for display; Path is preserved for traceability.
	DisplayPath string `json:"displayPath"`
	// Additions is the sum of per-edit additions across every edit
	// to this file.
	Additions int `json:"additions"`
	// Deletions is the sum of per-edit deletions across every edit
	// to this file.
	Deletions int `json:"deletions"`
	// EditCount is the number of edit/write tool calls that touched
	// this file.
	EditCount int `json:"editCount"`
	// FirstEditAt and LastEditAt are part.timeCreated unix ms for
	// the earliest and latest edits.
	FirstEditAt int64 `json:"firstEditAt"`
	LastEditAt  int64 `json:"lastEditAt"`
	// Patch is the concatenated unified-diff body for every edit on
	// this file in chronological order. Empty for legacy parts
	// (older OpenCode versions) that only carry Before/After.
	Patch string `json:"patch,omitempty"`
	// Before is the first edit's filediff.before snapshot. Populated
	// only by the legacy schema; empty when OpenCode emitted a
	// patch-style filediff instead. Frontend falls back to a
	// Before/After diff when Patch is empty.
	Before string `json:"before,omitempty"`
	// After is the last edit's filediff.after snapshot (or the Write
	// content). Same semantics as Before above.
	After string `json:"after,omitempty"`
	// Edits lists individual tool calls in chronological order.
	Edits []Edit `json:"edits"`
}

// Edit is one file-touching tool call. Patch is the unified-diff body
// for this single edit when OpenCode supplies one (newer schema).
// Before/After are the legacy snapshot pair, kept for backward
// compatibility with older parts in the database. Consumers prefer
// Patch when populated.
type Edit struct {
	PartID      string `json:"partId"`
	MessageID   string `json:"messageId"`
	TimeCreated int64  `json:"timeCreated"`
	// Tool is the lowercase tool name (e.g. "edit", "write",
	// "mcp_edit", "apply_patch"). Matches the value emitted on
	// Part.data.tool for OpenCode parts.
	Tool      string `json:"tool"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
}
