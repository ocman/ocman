package claudeprovider

import (
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

type subagentSession struct {
	session   db.Session
	root      string
	confirmed map[string]string
}

// Child lifecycle is separate from the parent's delegated-task ledger. Only
// agent tasks have conversations; shell and monitor task IDs are not sessions.
func (l *live) subagentEvent(p *proc, raw []byte) {
	var e struct {
		Subtype     string `json:"subtype"`
		TaskID      string `json:"task_id"`
		ToolUseID   string `json:"tool_use_id"`
		TaskType    string `json:"task_type"`
		Description string `json:"description"`
		Status      string `json:"status"`
		Patch       struct {
			Status string `json:"status"`
		} `json:"patch"`
	}
	if json.Unmarshal(raw, &e) != nil || e.TaskID == "" {
		return
	}
	child := "agent-" + e.TaskID
	if e.Subtype == "task_started" {
		if e.ToolUseID != "" && (e.TaskType == "local_agent" || e.TaskType == "") && p.children[e.ToolUseID] == nil {
			l.startSubagent(p, child, e.ToolUseID, e.Description)
		}
		return
	}
	status := e.Status
	if e.Subtype == "task_updated" {
		status = e.Patch.Status
	} else if e.Subtype != "task_notification" {
		return
	}
	var terminal db.SessionStatus
	switch status {
	case "completed":
		terminal = db.StatusDone
	case "failed":
		terminal = db.StatusError
	case "killed", "stopped":
		terminal = db.StatusInterrupted
	default:
		return
	}
	l.mu.Lock()
	active := l.status[child].Status == db.StatusBusy
	l.mu.Unlock()
	if !active {
		return
	}
	for _, c := range p.children {
		if c.SessionID == child {
			if info, _ := c.Live(); info != nil {
				l.publish(child, info, nil)
			}
			c.Finish()
			c.Messages, c.Parts = nil, nil
			break
		}
	}
	l.finishSubagent(child, terminal)
}

func (l *live) startSubagent(p *proc, child, call, title string) {
	parent := p.conv
	for _, c := range p.children {
		if c.cur != nil && c.cur.tools[call] != nil {
			parent = c
			break
		}
	}
	s := db.Session{ID: child, ParentID: parent.SessionID, Directory: p.dir, Title: title, TimeCreated: now(), TimeUpdated: now(), Status: db.StatusBusy}
	props, _ := json.Marshal(map[string]any{"sessionID": child, "info": map[string]any{
		"id": child, "parentID": s.ParentID, "directory": s.Directory, "title": title,
		"time": map[string]any{"created": s.TimeCreated, "updated": s.TimeUpdated},
	}})
	// Metadata must be available to the identity probe before consumers see
	// the first child event. It is never written to root-session ownership.
	if err := l.sup.Publish(providerprotocol.Event{Type: "session.created", Properties: props}, func() {
		l.mu.Lock()
		l.subagents[child] = subagentSession{session: s, root: p.id}
		l.subs[p.id] = append(l.subs[p.id], child)
		l.mu.Unlock()
	}); err != nil {
		l.publishFailed(p.id, err)
		return
	}
	p.children[call] = &Converter{SessionID: child}
	l.emit(child, "", "session.status", map[string]any{"sessionID": child, "status": map[string]string{"type": "busy"}})
	if part := parent.Link(call, child); part != nil {
		l.publish(parent.SessionID, nil, []map[string]any{part})
	}
}

func (l *live) finishSubagent(child string, status db.SessionStatus) {
	l.mu.Lock()
	_, known := l.subagents[child]
	active := l.status[child].Status == db.StatusBusy
	l.mu.Unlock()
	if !known || !active {
		return
	}
	var events []providerprotocol.Event
	if status != db.StatusDone {
		props, _ := json.Marshal(map[string]any{"sessionID": child, "status": map[string]string{"type": string(status)}})
		events = append(events, providerprotocol.Event{Type: "session.status", Properties: props})
	}
	props, _ := json.Marshal(map[string]any{"sessionID": child})
	events = append(events, providerprotocol.Event{Type: "session.idle", Properties: props})
	// Never publish idle after a failed terminal-status write. Parent recovery
	// retries children whose spool still has an open turn.
	if _, err := l.sup.PublishIf(func() bool { return true }, func(e providerprotocol.Event) { l.apply(child, "", e) }, events...); err != nil {
		l.publishFailed(child, err)
	}
}
