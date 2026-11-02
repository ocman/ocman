package claudeprovider

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/ocman/ocman/internal/db"
)

// scanState is the projection immediately before the last message group.
// Replaying that group includes appended tool results without rereading history.
type scanState struct {
	Entry                                            entry
	Custom, AI, Summary, Prompt, Current, LastFinish string
	LastClosed, LastErr                              bool
}

type indexCursor struct {
	State            scanState
	End, Size, Mtime int64
	Prefix, Boundary string
	LastID           string
	DuplicateReplay  bool
}

func (c *indexCursor) valid(f *os.File, st os.FileInfo, path string) bool {
	return c != nil && !c.DuplicateReplay && c.State.Entry.Path == path && c.End > 0 && c.End <= st.Size() &&
		c.State.Entry.End >= 0 && c.State.Entry.End <= c.End && c.State.Entry.Messages >= 0 && c.Size < st.Size() &&
		c.Prefix != "" && c.Boundary != "" &&
		c.Prefix == transcriptHash(f, 0, min(c.End, 4096)) &&
		c.Boundary == transcriptHash(f, max(0, c.End-4096), min(c.End, 4096))
}

func scanIndex(path, projects string, saved *indexCursor) (*entry, *indexCursor, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, false, err
	}
	delta := saved.valid(f, st, path)
	s := scanState{Entry: entry{Path: path}}
	if delta {
		s = saved.State
	}
	e := &s.Entry
	e.ID, e.RootID = identify(path, projects)
	e.Mtime, e.Size = st.ModTime().UnixNano(), st.Size()
	if e.ID != e.RootID {
		var meta struct {
			ToolUseID string `json:"toolUseId"`
		}
		if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
			_ = json.Unmarshal(data, &meta)
		}
		e.ParentToolUseID = meta.ToolUseID
	}
	prefix := s
	conv := &Converter{SessionID: e.ID}
	tools := map[string][]string{}
	usages := map[string]usage{}
	duplicateReplay := false
	r := bufio.NewReaderSize(io.NewSectionReader(f, e.End, st.Size()-e.End), 64<<10)
	for {
		raw, err := r.ReadBytes('\n')
		e.readBytes += int64(len(raw))
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, nil, false, err
		}
		var l line
		kind := skipLine
		decodeErr := json.Unmarshal(raw, &l)
		if decodeErr == nil {
			kind = classify(&l, s.Current)
		}
		if kind == startUser || kind == startAssistant {
			prefix = s
			_, duplicateReplay = usages[groupID(&l)]
		}
		start := e.End
		e.End += int64(len(raw))
		conv.Add(raw)
		toolTimes(conv.Parts, tools)
		conv.Parts = conv.Parts[:0]
		if decodeErr != nil {
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
		s.Custom, s.AI, s.Summary = or(l.CustomTitle, s.Custom), or(l.AITitle, s.AI), or(l.Summary, s.Summary)
		switch kind {
		case skipLine:
			continue
		case startUser:
			s.Current, s.LastErr = "", false
			text := ""
			for _, b := range l.blocks() {
				if b.Type == "text" {
					text = b.Text
					break
				}
			}
			s.LastClosed = closesTurn(text)
			if s.Prompt == "" {
				s.Prompt = promptTitle(text)
			}
		case startAssistant:
			s.Current = l.Message.ID
		}
		if kind != continueGroup {
			e.offsets = append(e.offsets, start)
			e.Messages++
			e.LatestID, e.LatestRole, e.LatestCreated = groupID(&l), l.Type, ts
		}
		if l.Type != "assistant" {
			continue
		}
		s.LastFinish, s.LastErr = finish(l.Message.StopReason), l.APIError
		e.Model = or(l.Message.Model, e.Model)
		id := groupID(&l)
		previous := usages[id]
		usages[id] = l.Message.Usage
		e.Input += l.Message.Usage.Input - previous.Input
		e.Output += l.Message.Usage.Output - previous.Output
		for _, b := range l.blocks() {
			if b.Type == "tool_use" && isTaskTool(b.Name) {
				e.tasks = append(e.tasks, b.ID)
			}
		}
	}
	conv.Finish()
	toolTimes(conv.Parts, tools)
	e.usage = analyticsRows(conv.Messages, tools)
	e.Title = or(s.Custom, or(s.AI, or(s.Summary, s.Prompt)))
	if e.Updated == 0 {
		e.Updated = st.ModTime().UnixMilli()
	}
	if e.Created == 0 {
		e.Created = e.Updated
	}
	e.Inferred = string(inferStatus(e.LatestRole, s.LastFinish, s.LastErr, s.LastClosed))
	after, err := os.Stat(path)
	if err != nil {
		return nil, nil, false, err
	}
	if !os.SameFile(st, after) || st.Size() != after.Size() || !st.ModTime().Equal(after.ModTime()) {
		return nil, nil, false, db.ErrHistoryChanged
	}
	// Private slices and parse counters are deliberately not persisted.
	prefix.Entry.offsets, prefix.Entry.tasks, prefix.Entry.usage = nil, nil, nil
	cursor := &indexCursor{State: prefix, End: e.End, Size: e.Size, Mtime: e.Mtime,
		LastID: e.LatestID, DuplicateReplay: duplicateReplay,
		Prefix: transcriptHash(f, 0, min(e.End, 4096)), Boundary: transcriptHash(f, max(0, e.End-4096), min(e.End, 4096))}
	return e, cursor, delta, nil
}
