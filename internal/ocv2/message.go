// Package ocv2 lets ocman drive an OpenCode v2 server through the v1
// HTTP surface the rest of ocman speaks.
//
// OpenCode v2 replaced the v1 API (sessions, messages and parts, the
// event stream, permissions and questions) with a typed /api/* surface
// and a new database schema. Rather than teach every caller a second
// dialect, ocman translates in one place:
//
//   - Wrap installs a RoundTripper under the shared OpenCode transport
//     that answers v1 routes for a v2 server (see compat.go, events.go).
//   - ConvertMessage maps a v2 message to the v1 `{info, parts}` shape;
//     the HTTP layer, the live event translator, and the database views
//     (internal/db) all use it, so the three always agree on ids.
//
// v1 servers are never touched: the RoundTripper only probes a server
// when the installed opencode binary is v2.
package ocv2

import (
	"fmt"
	"strings"
)

// V1Message is the v1 `GET /session/{id}/message` element.
type V1Message struct {
	Info  map[string]any   `json:"info"`
	Parts []map[string]any `json:"parts"`
}

// VisibleTypes are the v2 message types that have a v1 equivalent. The
// rest (system, idle, *-switched, skill) are bookkeeping v1 never stored.
var VisibleTypes = []string{"user", "synthetic", "assistant", "shell", "compaction"}

// PartID returns the stable v1 part id for the index-th part of a v2
// message. Index 0 is the synthetic step-start part of an assistant
// message; content item i is index i+1. The id keeps the message's
// ascending suffix so parts sort with their message.
func PartID(messageID string, index int) string {
	return fmt.Sprintf("prt%s%04d", strings.TrimPrefix(messageID, "msg"), index)
}

// ConvertMessage maps one v2 message (as served by the API, carrying
// `id` and `type`) to v1. ok is false for types v1 has no equivalent for.
func ConvertMessage(sessionID string, msg map[string]any) (V1Message, bool) {
	id := str(msg, "id")
	created := num(obj(msg, "time"), "created")
	base := func(role string) map[string]any {
		return map[string]any{"id": id, "sessionID": sessionID, "role": role, "time": map[string]any{"created": created}}
	}
	part := func(index int, fields map[string]any) map[string]any {
		fields["id"] = PartID(id, index)
		fields["messageID"] = id
		fields["sessionID"] = sessionID
		return fields
	}
	switch str(msg, "type") {
	case "user", "synthetic":
		info := base("user")
		if str(msg, "type") == "synthetic" {
			info["synthetic"] = true
		}
		parts := []map[string]any{part(1, map[string]any{"type": "text", "text": str(msg, "text"), "synthetic": str(msg, "type") == "synthetic"})}
		for i, f := range arr(msg, "files") {
			file, _ := f.(map[string]any)
			parts = append(parts, part(i+2, map[string]any{
				"type": "file", "mime": str(file, "mime"), "filename": str(file, "name"), "url": fileURL(file),
			}))
		}
		return V1Message{Info: info, Parts: parts}, true
	case "assistant":
		return convertAssistant(sessionID, msg, base("assistant"), part), true
	case "shell":
		info := base("assistant")
		info["agent"], info["mode"] = "build", "build"
		t := obj(msg, "time")
		info["time"] = span("created", created, "completed", t["completed"])
		output := str(obj(msg, "output"), "output")
		status := "completed"
		switch str(msg, "status") {
		case "running":
			status = "running"
		case "timeout", "killed":
			status = "error"
		}
		state := map[string]any{
			"status": status, "input": map[string]any{"command": str(msg, "command")},
			"output": output, "title": str(msg, "command"),
			"metadata": map[string]any{"output": output, "exit": msg["exit"]},
			"time":     span("start", created, "end", t["completed"]),
		}
		if status == "error" {
			state["error"] = str(msg, "status")
		}
		return V1Message{Info: info, Parts: []map[string]any{part(1, map[string]any{
			"type": "tool", "tool": "bash", "callID": str(msg, "shellID"), "state": state,
		})}}, true
	case "compaction":
		info := base("assistant")
		info["summary"] = true
		info["agent"], info["mode"] = "compaction", "compaction"
		if e := obj(msg, "error"); e != nil {
			info["error"] = v1Error(e)
		}
		usage(info, msg)
		if str(msg, "status") != "running" {
			info["finish"] = "stop"
		}
		return V1Message{Info: info, Parts: []map[string]any{part(1, map[string]any{"type": "text", "text": str(msg, "summary")})}}, true
	}
	return V1Message{}, false
}

func convertAssistant(sessionID string, msg, info map[string]any, part func(int, map[string]any) map[string]any) V1Message {
	model := obj(msg, "model")
	t := obj(msg, "time")
	info["agent"], info["mode"] = str(msg, "agent"), str(msg, "agent")
	info["providerID"], info["modelID"] = str(model, "providerID"), str(model, "id")
	if v := str(model, "variant"); v != "" {
		info["variant"] = v
	}
	info["time"] = span("created", t["created"], "completed", t["completed"])
	usage(info, msg)
	if f := str(msg, "finish"); f != "" {
		info["finish"] = f
	}
	if e := obj(msg, "error"); e != nil {
		info["error"] = v1Error(e)
	}
	parts := []map[string]any{part(0, map[string]any{"type": "step-start", "snapshot": str(obj(msg, "snapshot"), "start")})}
	content := arr(msg, "content")
	for i, c := range content {
		item, _ := c.(map[string]any)
		parts = append(parts, part(i+1, ConvertContent(item, t)))
	}
	if f := str(msg, "finish"); f != "" {
		parts = append(parts, part(len(content)+1, map[string]any{
			"type": "step-finish", "reason": f, "cost": info["cost"], "tokens": info["tokens"],
		}))
	}
	return V1Message{Info: info, Parts: parts}
}

// ConvertContent maps one assistant content item to a v1 part body (no
// ids). msgTime is the owning message's time, used where v2 has none.
func ConvertContent(item, msgTime map[string]any) map[string]any {
	switch str(item, "type") {
	case "text":
		return map[string]any{"type": "text", "text": str(item, "text"),
			"time": span("start", msgTime["created"], "end", msgTime["completed"])}
	case "reasoning":
		t := obj(item, "time")
		return map[string]any{"type": "reasoning", "text": str(item, "text"),
			"time": span("start", t["created"], "end", t["completed"])}
	case "tool":
		return convertTool(item)
	}
	return map[string]any{"type": str(item, "type")}
}

func usage(info, msg map[string]any) {
	info["cost"] = msg["cost"]
	if info["cost"] == nil {
		info["cost"] = 0.0
	}
	tokens := obj(msg, "tokens")
	if tokens == nil {
		tokens = map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}
	}
	info["tokens"] = tokens
}

// v1Error maps Session.StructuredError {type, message, status} to the
// v1 {name, data: {message, statusCode}} shape.
func v1Error(e map[string]any) map[string]any {
	data := map[string]any{"message": str(e, "message")}
	if s, ok := e["status"]; ok {
		data["statusCode"] = s
	}
	name := str(e, "type")
	if name == "" {
		name = "UnknownError"
	}
	return map[string]any{"name": name, "data": data}
}

func fileURL(file map[string]any) string {
	if src := obj(file, "source"); str(src, "type") == "uri" {
		return str(src, "uri")
	}
	if uri := str(file, "uri"); uri != "" {
		return uri
	}
	return "data:" + str(file, "mime") + ";base64," + str(file, "data")
}

// span builds a v1 {start|created, end|completed} time object, leaving
// out unset ends: v1 omitted them rather than sending null.
func span(startKey string, start any, endKey string, end any) map[string]any {
	t := map[string]any{startKey: start}
	if end != nil {
		t[endKey] = end
	}
	return t
}

// --- tiny accessors for decoded JSON ---

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}

func arr(m map[string]any, k string) []any {
	a, _ := m[k].([]any)
	return a
}

func num(m map[string]any, k string) any {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return m[k]
}
