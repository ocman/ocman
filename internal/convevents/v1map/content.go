package v1map

import (
	"bytes"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
)

// Lifecycle part types never reach the wire; step accounting rides on the
// message.
var lifecycleParts = map[string]bool{"step-start": true, "step-finish": true, "snapshot": true}

var editTools = map[string]bool{
	"edit": true, "write": true, "patch": true,
	"mcp_edit": true, "mcp_write": true, "mcp_Edit": true, "mcp_Write": true,
}

var questionTools = map[string]bool{"question": true, "mcp_question": true, "Question": true, "mcp_Question": true}

const maxAnchors = 4096

// messageData is the message field set the browser kept from v1 info.
type messageData struct {
	Role       string          `json:"role"`
	Finish     json.RawMessage `json:"finish,omitempty"`
	Summary    json.RawMessage `json:"summary,omitempty"`
	ModelID    json.RawMessage `json:"modelID,omitempty"`
	ProviderID json.RawMessage `json:"providerID,omitempty"`
	Agent      json.RawMessage `json:"agent,omitempty"`
	Mode       json.RawMessage `json:"mode,omitempty"`
	Cost       json.RawMessage `json:"cost,omitempty"`
	Tokens     json.RawMessage `json:"tokens,omitempty"`
	Time       json.RawMessage `json:"time,omitempty"`
	Error      json.RawMessage `json:"error,omitempty"`
}

func (m *Mapper) message(r *Result, p obj) {
	info := p.obj("info")
	id := info.str("id")
	if id == "" || !m.own(info.str("sessionID")) {
		return
	}
	pick := func(key string) json.RawMessage {
		if !info.has(key) {
			return nil
		}
		return info[key]
	}
	data := messageData{Role: info.first("role"), Finish: pick("finish"), Summary: pick("summary"), ModelID: pick("modelID"),
		ProviderID: pick("providerID"), Agent: pick("agent"), Mode: pick("mode"), Tokens: pick("tokens"),
		Time: pick("time"), Error: pick("error")}
	if data.Role == "" {
		data.Role = "assistant"
	}
	if _, ok := info.num("cost"); ok {
		data.Cost = info["cost"]
	}
	created, ok := info.obj("time").num("created")
	if !ok {
		created = float64(m.opts.Now())
	}
	m.emit(r, convevents.MessageUpserted{Message: db.Message{ID: id, SessionID: m.opts.SessionID,
		TimeCreated: int64(created), Data: mustMarshal(data)}})
	for _, raw := range p.arr("parts") {
		m.part(r, raw, id)
	}
}

func (m *Mapper) partUpdated(r *Result, p obj) { m.part(r, p["part"], "") }

func (m *Mapper) part(r *Result, raw json.RawMessage, messageID string) {
	part := parseObj(raw)
	id, mid := part.str("id"), part.first("messageID", "messageId")
	if mid == "" {
		mid = messageID
	}
	typ := part.str("type")
	if id == "" || mid == "" || lifecycleParts[typ] || !m.own(part.str("sessionID")) {
		return
	}
	var data bytes.Buffer
	if json.Compact(&data, raw) != nil {
		return
	}
	final := isFinal(typ, part)
	m.anchor(id, mid, typ, part, final)
	start, _ := part.obj("time").num("start")
	if start == 0 {
		start, _ = part.obj("state").obj("time").num("start")
	}
	m.emit(r, convevents.PartUpserted{Final: final, Part: convevents.Part{Part: db.Part{
		ID: id, MessageID: mid, SessionID: m.opts.SessionID, TimeCreated: int64(start), Data: data.Bytes()}}})
	if typ != "tool" {
		return
	}
	tool, state := part.str("tool"), part.obj("state")
	if questionTools[tool] {
		if rid := answeredQuestion(state); rid != "" && !m.resolved[rid] {
			m.resolved[rid] = true
			m.emit(r, convevents.PromptResolved{PromptID: rid, Kind: convevents.PromptQuestion,
				Outcome: convevents.ResolvedAnswered, By: convevents.ByExternal})
		}
	}
	if editTools[tool] && state.str("status") == "completed" && !m.dirtied[id] {
		m.dirtied[id] = true
		m.emit(r, convevents.ChangesDirty{Reason: convevents.DirtyEdit})
	}
}

// anchor records a part's streaming fields. A final upsert replaces them; a
// non-final one keeps the longer of local and snapshot, as the client does.
func (m *Mapper) anchor(id, messageID, typ string, part obj, final bool) {
	st := m.parts[id]
	if st == nil {
		if len(m.parts) >= maxAnchors {
			m.removeAnchor(m.partOrder.Front().Value.(string))
		}
		st = &partState{fields: map[string]int{}, order: m.partOrder.PushBack(id)}
		m.parts[id] = st
	}
	m.partOrder.MoveToBack(st.order)
	st.messageID, st.typ = messageID, typ
	for field, value := range map[string]string{
		convevents.FieldText:       part.str("text"),
		convevents.FieldToolOutput: part.obj("state").str("output"),
	} {
		length := convevents.UTF16Len(value)
		if final || length >= st.fields[field] {
			st.fields[field] = length
		}
	}
}

func isFinal(typ string, part obj) bool {
	switch typ {
	case "tool":
		s := part.obj("state").str("status")
		return s == "completed" || s == "error"
	case "text", "reasoning":
		end, _ := part.obj("time").num("end")
		return end != 0
	case "file", "patch", "agent":
		return true
	}
	return false
}

// answeredQuestion returns the request id of a question tool part the user
// answered (possibly outside ocman), else "".
func answeredQuestion(state obj) string {
	if s := state.str("status"); s == "running" || s == "pending" {
		return ""
	}
	out, ok := state["output"]
	if !ok || string(out) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(out, &s) == nil && (s == "" || s == `""` || s == "[]") {
		return ""
	}
	if id := state.obj("input").first("requestId", "requestID", "id"); id != "" {
		return id
	}
	return state.obj("metadata").first("requestId", "requestID", "id")
}

var deltaFields = map[string]string{"text": convevents.FieldText, "state.output": convevents.FieldToolOutput}

func (m *Mapper) delta(r *Result, p obj) {
	if !m.own(p.str("sessionID")) {
		return
	}
	mid, pid, text := p.str("messageID"), p.str("partID"), p.str("delta")
	name := p.str("field")
	if name == "" {
		name = "text"
	}
	field, ok := deltaFields[name]
	if !ok || mid == "" || pid == "" || text == "" {
		return
	}
	m.idle = false
	st := m.parts[pid]
	if st == nil {
		r.Unanchored = append(r.Unanchored, PartRef{MessageID: mid, PartID: pid})
		return
	}
	offset := st.fields[field]
	st.fields[field] += convevents.UTF16Len(text)
	m.partOrder.MoveToBack(st.order)
	m.emit(r, convevents.PartDelta{MessageID: mid, PartID: pid, Field: field, Offset: offset, Text: text, PartType: st.typ})
}

func (m *Mapper) messageRemoved(r *Result, p obj) {
	mid := p.first("messageID", "messageId")
	if mid == "" || !m.own(p.str("sessionID")) {
		return
	}
	for id, st := range m.parts {
		if st.messageID == mid {
			m.removeAnchor(id)
		}
	}
	m.emit(r, convevents.MessageRemoved{MessageID: mid, Reason: convevents.RemovedDeleted})
}

func (m *Mapper) partRemoved(r *Result, p obj) {
	mid, pid := p.first("messageID", "messageId"), p.first("partID", "partId")
	if mid == "" || pid == "" || !m.own(p.str("sessionID")) {
		return
	}
	m.removeAnchor(pid)
	m.emit(r, convevents.PartRemoved{MessageID: mid, PartID: pid})
}

func (m *Mapper) removeAnchor(id string) {
	if st := m.parts[id]; st != nil {
		m.partOrder.Remove(st.order)
		delete(m.parts, id)
	}
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
