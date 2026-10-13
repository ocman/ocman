package claudeprovider

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
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
}

// identify derives a transcript's session identity from its path:
// <project>/<session>.jsonl, or <project>/<session>/subagents/agent-<id>.jsonl.
func identify(path string) (id, root string) {
	id = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "subagents" {
		return id, filepath.Base(filepath.Dir(dir))
	}
	return id, id
}

// scanFile reads a whole transcript once. A trailing partial line (still
// being written) is left for the next scan.
func scanFile(path string) (*entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	e := &entry{Path: path, Mtime: st.ModTime().UnixNano(), Size: st.Size()}
	e.ID, e.RootID = identify(path)
	if e.ID != e.RootID {
		var meta struct {
			ToolUseID string `json:"toolUseId"`
		}
		if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
			_ = json.Unmarshal(data, &meta)
		}
		e.ParentToolUseID = meta.ToolUseID
	}
	var custom, ai, summary, prompt, current, lastFinish string
	var lastClosed, lastErr bool
	usages := map[string]usage{}
	tools := map[string][]string{} // message id -> tool state.time JSON
	// Analytics rows come from the one converter (AD-12); parts are dropped
	// as they are produced. ponytail: a second JSON parse per line.
	conv := &Converter{SessionID: e.ID}
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		start := e.End
		e.End += int64(len(raw))
		conv.Add(raw)
		toolTimes(conv.Parts, tools)
		conv.Parts = conv.Parts[:0]
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		ts := l.millis()
		if ts > 0 && e.Created == 0 {
			e.Created = ts
		}
		e.Updated = max(e.Updated, ts)
		if e.Directory == "" {
			e.Directory = l.Cwd
		}
		custom, ai, summary = or(l.CustomTitle, custom), or(l.AITitle, ai), or(l.Summary, summary)
		kind := classify(&l, current)
		switch kind {
		case skipLine:
			continue
		case startUser:
			current, lastErr = "", false
			text := ""
			for _, b := range l.blocks() {
				if b.Type == "text" {
					text = b.Text
					break
				}
			}
			lastClosed = closesTurn(text)
			if prompt == "" {
				prompt = promptTitle(text)
			}
		case startAssistant:
			current = l.Message.ID
		}
		if kind != continueGroup {
			e.offsets = append(e.offsets, start)
			e.LatestID, e.LatestRole, e.LatestCreated = groupID(&l), l.Type, ts
		}
		if l.Type != "assistant" {
			continue
		}
		lastFinish, lastErr = finish(l.Message.StopReason), l.APIError
		e.Model = or(l.Message.Model, e.Model)
		usages[groupID(&l)] = l.Message.Usage
		for _, b := range l.blocks() {
			if b.Type == "tool_use" && isTaskTool(b.Name) {
				e.tasks = append(e.tasks, b.ID)
			}
		}
	}
	conv.Finish()
	toolTimes(conv.Parts, tools)
	e.usage = analyticsRows(conv.Messages, tools)
	for _, u := range usages {
		e.Input += u.Input
		e.Output += u.Output
	}
	e.Messages = len(e.offsets)
	e.Title = or(custom, or(ai, or(summary, prompt)))
	if e.Updated == 0 {
		e.Updated = st.ModTime().UnixMilli()
	}
	if e.Created == 0 {
		e.Created = e.Updated
	}
	e.Inferred = string(inferStatus(e.LatestRole, lastFinish, lastErr, lastClosed))
	return e, nil
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
// overlay busy, limited and error.
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
