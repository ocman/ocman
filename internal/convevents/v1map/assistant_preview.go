package v1map

import "strings"

// AssistantPreview projects a live stream into bounded, replaceable text. It
// does not use the browser mapper's permissive defaults for role or session.
// A delta needs an explicit assistant message and a visible text part anchor.
type AssistantPreview struct {
	SessionID         string
	PreviousMessageID string
	MaxBytes          int
	message           string
	user              string
	parts             []previewPart
	bytes             int
	blocked           bool
}

type previewPart struct{ id, text string }

// Apply reports a changed snapshot, or the end of the observed turn. It never
// uses acknowledged history to seed a new preview.
func (p *AssistantPreview) Apply(f Frame) (message, text string, done bool) {
	typ, props, top := normalize(f)
	if string(top["acknowledged"]) == "true" {
		return "", "", false
	}
	var changed bool
	switch typ {
	case "message.created", "message.updated":
		info := props.obj("info")
		id := info.str("id")
		if info.str("sessionID") != p.SessionID || id == "" || id == p.PreviousMessageID {
			break
		}
		if info.str("role") == "user" && string(info["synthetic"]) != "true" {
			if id != p.user && (p.user != "" || p.message != "") {
				return "", "", true
			}
			p.user = id
		}
		if info.str("role") != "assistant" || string(info["synthetic"]) == "true" {
			if id == p.message {
				p.blocked = true
			}
			break
		}
		if id != p.message {
			if info.obj("time").int("completed") != 0 {
				break
			}
			p.message, p.parts, p.bytes, p.blocked = id, nil, 0, false
		}
		for _, part := range props.arr("parts") {
			changed = p.part(parseObj(part), id) || changed
		}
	case "message.part.updated":
		changed = p.part(props.obj("part"), "")
	case "message.part.delta":
		if props.str("sessionID") != p.SessionID || props.str("messageID") != p.message || props.str("field") != "text" {
			break
		}
		for i, part := range p.parts {
			if part.id == props.str("partID") {
				delta := props.str("delta")
				if len(delta) > p.MaxBytes-p.bytes {
					p.blocked = true
					break
				}
				changed = delta != "" && p.replace(i, part.text+delta)
				break
			}
		}
	case "message.removed", "message.part.removed":
		if props.str("sessionID") == p.SessionID && props.str("messageID") == p.message {
			p.blocked = true
		}
	case "session.idle", "session.status":
		idle := typ == "session.idle" || props.obj("status").str("type") == "idle" || props.str("status") == "idle"
		return "", "", idle && props.str("sessionID") == p.SessionID && (p.message != "" || p.user != "")
	}
	if !changed || p.blocked {
		return "", "", false
	}
	var out strings.Builder
	out.Grow(p.bytes)
	for _, part := range p.parts {
		out.WriteString(part.text)
	}
	return p.message, out.String(), false
}

func (p *AssistantPreview) part(part obj, parent string) bool {
	mid := part.first("messageID", "messageId")
	if mid == "" {
		mid = parent
	}
	sid := part.str("sessionID")
	if sid == "" && parent != "" {
		sid = p.SessionID // embedded parts inherit the verified message session
	}
	id, text := part.str("id"), part.str("text")
	if p.blocked || mid == "" || mid != p.message || id == "" || sid != p.SessionID {
		return false
	}
	visible := part.str("type") == "text" && string(part["synthetic"]) != "true" && string(part["ignored"]) != "true"
	for i, prior := range p.parts {
		if prior.id == id {
			if !visible {
				p.blocked = true
				return false
			}
			// Nonfinal upserts may lag deltas. Use the final upsert if it differs.
			if part.obj("time").int("end") == 0 && strings.HasPrefix(prior.text, text) {
				return false
			}
			return p.replace(i, text)
		}
	}
	if !visible {
		return false
	}
	if len(p.parts) >= 64 || len(text) > p.MaxBytes-p.bytes {
		p.blocked = true
		return false
	}
	p.parts = append(p.parts, previewPart{id: id, text: text})
	p.bytes += len(text)
	return text != ""
}

func (p *AssistantPreview) replace(i int, text string) bool {
	old := p.parts[i].text
	n := p.bytes - len(old) + len(text)
	if n > p.MaxBytes {
		p.blocked = true
		return false
	}
	p.parts[i].text, p.bytes = text, n
	return old != text
}
