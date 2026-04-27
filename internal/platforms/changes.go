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

// FileChange is the per-file roll-up. Before/After are the first edit's
// before-snapshot and the last edit's after-snapshot respectively, so
// rendering a single unified diff between them produces the
// "current vs. original" view the sidebar shows. The summed
// Additions/Deletions are authoritative — they may differ from the
// line counts of the collapsed diff when an intermediate edit was
// reverted later in the session.
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
	// Before is the first edit's filediff.before (full file content
	// before the session touched it). Empty when the first
	// observed operation was a Write to a new file.
	Before string `json:"before"`
	// After is the last edit's filediff.after (or the Write
	// content). Empty if no after-snapshot was captured.
	After string `json:"after"`
	// Edits lists individual tool calls in chronological order.
	Edits []Edit `json:"edits"`
}

// Edit is one file-touching tool call. Before/After are populated
// best-effort: OpenCode supplies them via state.metadata.filediff;
// when missing, the frontend can compute a hunk from
// state.input.oldString/newString instead.
type Edit struct {
	PartID      string `json:"partId"`
	MessageID   string `json:"messageId"`
	TimeCreated int64  `json:"timeCreated"`
	// Tool is the lowercase tool name (e.g. "edit", "write",
	// "mcp_edit"). Matches the value emitted on
	// Part.data.tool for OpenCode parts.
	Tool      string `json:"tool"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
}
