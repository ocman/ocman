package v1map

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Frame is one SSE frame of a v1-shaped session stream.
type Frame struct {
	// Event is the SSE event name; "" and "message" mean the default channel.
	Event string `json:"event,omitempty"`
	// ID is the SSE id (the provider supervisor sequence for Claude).
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data"`
}

// ParseFrame reads one SSE frame (without its blank-line terminator).
// Multi-line data is joined with newlines; comments are skipped.
func ParseFrame(block []byte) Frame {
	var f Frame
	var data []string
	for _, line := range strings.Split(string(block), "\n") {
		line = strings.TrimSuffix(line, "\r")
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "event":
			f.Event = value
		case "id":
			f.ID = value
		case "data":
			data = append(data, value)
		}
	}
	if data != nil {
		f.Data = json.RawMessage(strings.Join(data, "\n"))
	}
	return f
}

// obj is a JSON object with lazily decoded members.
type obj map[string]json.RawMessage

func parseObj(raw []byte) obj {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var o obj
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	return o
}

func (o obj) obj(key string) obj { return parseObj(o[key]) }

func (o obj) str(key string) string {
	var s string
	if json.Unmarshal(o[key], &s) != nil {
		return ""
	}
	return s
}

// first returns the first non-empty string member among keys.
func (o obj) first(keys ...string) string {
	for _, k := range keys {
		if s := o.str(k); s != "" {
			return s
		}
	}
	return ""
}

func (o obj) num(key string) (float64, bool) {
	var n float64
	if json.Unmarshal(o[key], &n) != nil {
		return 0, false
	}
	return n, true
}

func (o obj) int(key string) int64 {
	n, _ := o.num(key)
	return int64(n)
}

func (o obj) arr(key string) []json.RawMessage {
	var a []json.RawMessage
	if json.Unmarshal(o[key], &a) != nil {
		return nil
	}
	return a
}

func (o obj) has(key string) bool {
	v, ok := o[key]
	return ok && string(bytes.TrimSpace(v)) != "null"
}

func (o obj) strings(key string) []string {
	out := []string{}
	for _, raw := range o.arr(key) {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// normalize resolves a frame to its v1 type and properties the way the
// browser's parseEvent did: global {directory, payload} wrappers unwrap,
// and named channels carrying bare payloads take the channel as type.
func normalize(f Frame) (typ string, props obj, top obj) {
	top = parseObj(f.Data)
	if top == nil {
		return "", nil, nil
	}
	if !top.has("properties") {
		if payload := top.obj("payload"); payload != nil {
			if payload.str("type") != "" && payload.obj("properties") != nil {
				top = payload
			} else {
				top = obj{"type": top["type"], "properties": top["payload"]}
			}
		}
	}
	typ = top.str("type")
	props = top.obj("properties")
	channel := f.Event
	if channel == "" || channel == "message" {
		if props == nil {
			props = top
		}
		return typ, props, top
	}
	bare := top.str("id") != "" && top.first("messageID", "messageId") != ""
	if typ == "" || bare || (channel == "message.part.updated" && typ != channel) {
		typ = channel
		if props == nil {
			props = top
			if channel == "message.part.updated" {
				raw, _ := json.Marshal(top)
				props = obj{"part": raw}
			}
		}
	}
	if props == nil {
		props = top
	}
	return typ, props, top
}
