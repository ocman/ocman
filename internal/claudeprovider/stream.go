package claudeprovider

import (
	"encoding/json"
	"time"
)

// Stream converts one --include-partial-messages stream_event and returns
// what changed: the message a message_start opened, or the part a block
// start/delta grew. Partial parts carry their block's final id (the stream's
// content_block index is the converter's block ordinal), so the complete
// assistant line replaces them in place. Tool input deltas are not streamed;
// the tool part appears with the complete line.
func (c *Converter) Stream(raw []byte) (info, part map[string]any) {
	var s struct {
		Event struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			Block struct {
				Type string `json:"type"`
			} `json:"content_block"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil, nil
	}
	e, ts := s.Event, time.Now().UnixMilli()
	if e.Type == "message_start" {
		if e.Message.ID == "" {
			return nil, nil
		}
		if c.cur == nil || c.cur.info["id"] != e.Message.ID {
			c.Finish()
			c.start(e.Message.ID, "assistant", ts)
			c.cur.info["providerID"], c.cur.info["modelID"] = "anthropic", e.Message.Model
		}
		return c.cur.info, nil
	}
	if c.cur == nil || c.cur.info["role"] != "assistant" {
		return nil, nil
	}
	var kind, text string
	switch {
	case e.Type == "content_block_start" && e.Block.Type == "text", e.Delta.Type == "text_delta":
		kind, text = "text", e.Delta.Text
	case e.Type == "content_block_start" && e.Block.Type == "thinking", e.Delta.Type == "thinking_delta":
		kind, text = "reasoning", e.Delta.Thinking
	default:
		return nil, nil
	}
	p := c.partial(e.Index, kind, ts)
	if p == nil {
		return nil, nil
	}
	p["text"] = p["text"].(string) + text
	return nil, p
}

// partial returns the streamed part for block index, creating it. A block
// whose complete line already arrived is not streamed again.
func (c *Converter) partial(index int, kind string, ts int64) map[string]any {
	g := c.cur
	if index < g.block {
		return nil
	}
	msg := g.info["id"].(string)
	id := partID(msg, index)
	if i := g.find(id); i >= 0 {
		return g.parts[i]
	}
	// ponytail: thinking the CLI later stores empty (signature only) keeps
	// its streamed text live until the transcript is re-read.
	p := map[string]any{"id": id, "messageID": msg, "sessionID": c.SessionID, "type": kind, "text": "",
		"time": map[string]any{"start": ts}}
	g.parts, g.times = append(g.parts, p), append(g.times, ts)
	return p
}

// Link records that a Task/Agent call spawned the child session and returns
// the call's open part with the link in its metadata, nil when it is not open.
func (c *Converter) Link(toolUseID, child string) map[string]any {
	if c.Children == nil {
		c.Children = map[string]string{}
	}
	c.Children[toolUseID] = child
	if c.cur == nil || c.cur.tools[toolUseID] == nil {
		return nil
	}
	c.cur.tools[toolUseID]["metadata"].(map[string]any)["sessionId"] = child
	for _, p := range c.cur.parts {
		if p["callID"] == toolUseID {
			return p
		}
	}
	return nil
}

// publish upserts a live message and its changed parts into session id's view.
func (l *live) publish(id string, info map[string]any, parts []map[string]any) {
	if info != nil {
		l.emit(id, "m:"+info["id"].(string), "message.updated", map[string]any{"info": info})
	}
	for _, part := range parts {
		l.emit(id, "p:"+part["id"].(string), "message.part.updated", map[string]any{"part": part})
	}
}

// system handles the CLI's system lines: task_started links a subagent to its
// spawning call (its transcript is subagents/agent-<task_id>.jsonl, the
// index's child id) and init reports the session's skills.
func (l *live) system(p *proc, raw []byte) {
	var s struct {
		Subtype   string   `json:"subtype"`
		TaskID    string   `json:"task_id"`
		ToolUseID string   `json:"tool_use_id"`
		Skills    []string `json:"skills"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return
	}
	switch s.Subtype {
	case "init":
		l.cmds.observeSkills(p.dir, s.Skills)
	case "task_started":
		if s.TaskID == "" || s.ToolUseID == "" || p.children[s.ToolUseID] != nil {
			return
		}
		child := "agent-" + s.TaskID
		convs := []*Converter{p.conv}
		for _, c := range p.children {
			convs = append(convs, c) // a nested subagent's call is in its parent child
		}
		p.children[s.ToolUseID] = &Converter{SessionID: child}
		l.mu.Lock()
		l.subs[p.id] = append(l.subs[p.id], child)
		l.mu.Unlock()
		for _, c := range convs {
			if part := c.Link(s.ToolUseID, child); part != nil {
				l.publish(c.SessionID, nil, []map[string]any{part})
			}
		}
	}
}

// started records that the CLI began an input (its replay echo): the session
// now has a transcript, so a respawn must --resume it even if this first
// turn never reaches its result.
//
// A failed update latches the turn as failed and stops the child: Claude
// has a transcript ocman could not record, so the turn must not settle as
// success, and the next launch records it before resuming (spawnLocked).
func (l *live) started(p *proc, input string) {
	l.mu.Lock()
	delete(p.pending, input)
	err := l.markStartedLocked(p.id)
	if err != nil {
		p.startErr = err
		l.unrecorded[p.id] = true
		_ = p.cmd.Process.Kill()
	}
	l.mu.Unlock()
	if err != nil {
		l.errorMessage(p.id, err.Error())
	}
}

// markStartedLocked records that the session has a transcript to --resume.
// A failure is returned: a respawn would otherwise reuse --session-id.
func (l *live) markStartedLocked(id string) error {
	l.syncOwnedLocked()
	if o := l.owned[id]; o == nil || (o.Started && o.ForkOf == "") {
		return nil
	}
	return l.updateOwnedLocked(func() {
		if o := l.owned[id]; o != nil {
			o.Started, o.ForkOf = true, ""
		}
	})
}
