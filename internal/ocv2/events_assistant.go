package ocv2

import (
	"net/http"
	"net/url"
)

// ephemeral v2 events are never projected into the stored message, so
// they still apply on top of a freshly fetched snapshot.
var ephemeral = map[string]bool{
	"session.text.delta": true, "session.reasoning.delta": true,
	"session.tool.input.delta": true, "session.tool.progress": true,
}

// assistant applies one step/text/reasoning/tool event to the in-flight
// assistant message the way OpenCode's projector does, then emits the
// affected v1 message or part.
func (t *translator) assistant(ev v2Event, sessionID, dir string) error {
	d := ev.Data
	mid := str(d, "assistantMessageID")
	if mid == "" {
		return nil
	}
	msg := t.msgs[mid]
	if ev.Type == "session.step.started" {
		if msg == nil {
			// A retried step reuses its message and keeps the earlier
			// attempt's content; load it so new parts get the indexes the
			// stored message will have. Unknown (new) messages start empty.
			delete(t.unseeded, mid)
			msg = t.seed(sessionID, mid)
		}
		if msg == nil {
			msg = map[string]any{"id": mid, "type": "assistant", "sessionID": sessionID, "content": []any{}}
			t.msgs[mid] = msg
		}
		msg["agent"], msg["model"] = d["agent"], d["model"]
		msg["time"] = map[string]any{"created": d["started"]}
		msg["snapshot"] = map[string]any{"start": d["snapshot"]}
		delete(msg, "finish")
		delete(msg, "error")
		return t.emitMessage(sessionID, dir, msg, 0, true)
	}
	if msg == nil {
		if t.unseeded[mid] {
			return nil
		}
		seeded := t.seed(sessionID, mid)
		if seeded == nil {
			t.unseeded[mid] = true
			return nil
		}
		msg = seeded
		if err := t.emitMessage(sessionID, dir, msg, -1, true); err != nil || !ephemeral[ev.Type] {
			return err
		}
	}
	content, _ := msg["content"].([]any)
	partIndex := func(i int) int { return i + 1 }
	last := func(typ string, open bool) int {
		for i := len(content) - 1; i >= 0; i-- {
			c, _ := content[i].(map[string]any)
			if str(c, "type") == typ && (!open || obj(c, "time")["completed"] == nil) {
				return i
			}
		}
		return -1
	}
	tool := func() map[string]any {
		for i := len(content) - 1; i >= 0; i-- {
			c, _ := content[i].(map[string]any)
			if str(c, "type") == "tool" && str(c, "id") == str(d, "id") {
				return c
			}
		}
		return nil
	}
	toolIndex := func(item map[string]any) int {
		for i := range content {
			if c, _ := content[i].(map[string]any); str(c, "id") == str(item, "id") && str(c, "type") == "tool" {
				return i
			}
		}
		return -1
	}
	emitPart := func(i int) error {
		if i < 0 {
			return nil
		}
		return t.emitMessage(sessionID, dir, msg, partIndex(i), false)
	}
	delta := func(i int) error {
		if i < 0 {
			return nil
		}
		c, ok := content[i].(map[string]any)
		if !ok {
			return nil
		}
		c["text"] = str(c, "text") + str(d, "delta")
		return t.emit(sessionID, dir, "message.part.delta", map[string]any{
			"messageID": mid, "partID": PartID(mid, partIndex(i)), "field": "text", "delta": str(d, "delta"),
		})
	}
	tm, _ := msg["time"].(map[string]any)
	switch ev.Type {
	case "session.step.ended", "session.step.failed":
		for _, k := range []string{"finish", "cost", "tokens", "error"} {
			if v, ok := d[k]; ok {
				msg[k] = v
			}
		}
		// OpenCode's projector records a failed step as finish "error".
		if ev.Type == "session.step.failed" && str(d, "finish") == "" {
			msg["finish"] = "error"
		}
		if tm != nil {
			tm["completed"] = ev.Created
		}
		return t.emitMessage(sessionID, dir, msg, len(content)+1, true)
	case "session.text.started":
		content = append(content, map[string]any{"type": "text", "text": ""})
		msg["content"] = content
		return emitPart(len(content) - 1)
	case "session.reasoning.started":
		content = append(content, map[string]any{"type": "reasoning", "text": "", "time": map[string]any{"created": ev.Created}})
		msg["content"] = content
		return emitPart(len(content) - 1)
	case "session.text.delta":
		return delta(last("text", false))
	case "session.reasoning.delta":
		return delta(last("reasoning", true))
	case "session.text.ended", "session.reasoning.ended":
		typ := "text"
		if ev.Type == "session.reasoning.ended" {
			typ = "reasoning"
		}
		i := last(typ, typ == "reasoning")
		if i < 0 {
			return nil
		}
		c, ok := content[i].(map[string]any)
		if !ok {
			return nil
		}
		c["text"] = str(d, "text")
		if typ == "reasoning" {
			timeOf(c)["completed"] = ev.Created
		}
		return emitPart(i)
	case "session.tool.input.started":
		content = append(content, map[string]any{
			"type": "tool", "id": str(d, "id"), "name": str(d, "name"),
			"state": map[string]any{"status": "streaming", "input": ""}, "time": map[string]any{"created": ev.Created},
		})
		msg["content"] = content
		return emitPart(len(content) - 1)
	case "session.tool.called", "session.tool.progress", "session.tool.success", "session.tool.failed":
		item := tool()
		if item == nil {
			return nil
		}
		state := obj(item, "state")
		input := state["input"]
		switch ev.Type {
		case "session.tool.called":
			item["state"] = map[string]any{"status": "running", "input": d["input"], "metadata": map[string]any{}}
			timeOf(item)["ran"] = ev.Created
		case "session.tool.progress":
			state["metadata"] = d["metadata"]
		case "session.tool.success":
			item["state"] = map[string]any{"status": "completed", "input": input, "content": d["content"], "metadata": d["metadata"]}
			timeOf(item)["completed"] = ev.Created
		case "session.tool.failed":
			item["state"] = map[string]any{"status": "error", "input": input, "error": d["error"], "content": d["content"], "metadata": d["metadata"]}
			timeOf(item)["completed"] = ev.Created
		}
		return emitPart(toolIndex(item))
	}
	return nil
}

// seed loads an assistant message that was already in flight when this
// stream connected.
func (t *translator) seed(sessionID, mid string) map[string]any {
	var resp data[map[string]any]
	path := "/api/session/" + url.PathEscape(sessionID) + "/message/" + url.PathEscape(mid)
	if t.c.call(t.ctx, http.MethodGet, path, nil, nil, &resp) != nil || resp.Data == nil {
		return nil
	}
	resp.Data["sessionID"] = sessionID
	t.msgs[mid] = resp.Data
	return resp.Data
}

// timeOf returns item.time, creating it so projections never write to a nil map.
func timeOf(item map[string]any) map[string]any {
	t := obj(item, "time")
	if t == nil {
		t = map[string]any{}
		item["time"] = t
	}
	return t
}
