package claudeprovider

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/db"
)

// entry is one indexed transcript: the list projection, message offsets and
// the Task/Agent calls it made (for subagent -> parent links).
type entry struct {
	Path, ID, RootID, ParentID, ParentToolUseID string
	Mtime, Size, End                            int64 // End: bytes of complete lines
	Title, Directory, Model, Inferred           string
	Created, Updated, Input, Output             int64
	LatestID, LatestRole                        string
	LatestCreated                               int64
	Messages                                    int
	offsets                                     []int64
	tasks                                       []string
	usage                                       []db.AnalyticsMessage
	readBytes                                   int64
}

// identify derives a transcript's session identity from its path:
// <project>/<session>.jsonl, or <project>/<session>/subagents/agent-<id>.jsonl.
func identify(path, projects string) (id, root string) {
	id = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	rel, err := filepath.Rel(projects, path)
	parts := strings.Split(rel, string(filepath.Separator))
	if err == nil && len(parts) >= 4 && parts[0] != ".." && parts[2] == "subagents" {
		return id, parts[1]
	}
	return id, id
}

// scanFile reads a whole transcript once. A trailing partial line (still
// being written) is left for the next scan.
func scanFile(path, projects string) (*entry, error) {
	e, _, _, err := scanIndex(path, projects, nil)
	return e, err
}

// inferStatus reads the transcript tail the way InferSessionStatus reads
// OpenCode's last message. An open prompt or a tool-calls step is an
// unfinished turn; SettleSessionStatus decides busy vs interrupted.
func inferStatus(role, fin string, apiErr, closed bool) db.SessionStatus {
	errText := ""
	if apiErr {
		errText = "api error"
	}
	switch {
	case role == "user" && !closed:
		return db.InferSessionStatus("assistant", "", "", false)
	case role == "assistant" && fin == "tool-calls":
		return db.InferSessionStatus("assistant", "", errText, false)
	case role == "assistant":
		return db.InferSessionStatus("assistant", fin, errText, false)
	}
	return db.InferSessionStatus(role, "", "", false)
}

// session settles the transcript with no live view (AD-10): a write time is
// not a lifecycle signal, so an open turn nothing observes reads as
// interrupted. The process (owned sessions) or hooks (terminal sessions)
// overlay busy and settled turn outcomes.
func (e *entry) session(time.Time) db.Session {
	return db.Session{
		ID: e.ID, ParentID: e.ParentID, Title: e.Title, Directory: e.Directory,
		TimeCreated: e.Created, TimeUpdated: e.Updated, MessageCount: e.Messages,
		TotalInputTokens: e.Input, TotalOutputTokens: e.Output,
		Status: db.SettleSessionStatus(db.TurnUnobserved, false, db.SessionStatus(e.Inferred)),
	}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
