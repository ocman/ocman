package convstream

import (
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
)

// overlay is the session's live state, restated by stream.hello. Every
// field is absolute, so a later event of the same kind replaces it.
type overlay struct {
	prompts     []convevents.Prompt
	judge       map[string]convevents.PromptJudge
	resolved    map[string]bool
	background  convevents.BackgroundChanged
	queue       *convevents.QueueChanged
	liveConnect bool
}

func newOverlay() overlay {
	return overlay{judge: map[string]convevents.PromptJudge{}, resolved: map[string]bool{},
		background: convevents.BackgroundChanged{Tasks: []convevents.BackgroundTask{}}}
}

// ask opens a prompt; a repeat replaces it in place. It reports false for a
// prompt already resolved in this epoch.
func (o *overlay) ask(p convevents.Prompt) bool {
	if o.resolved[p.ID] {
		return false
	}
	for i := range o.prompts {
		if o.prompts[i].ID == p.ID {
			o.prompts[i] = p
			return true
		}
	}
	o.prompts = append(o.prompts, p)
	return true
}

// resolve closes a prompt. It reports false for a repeat resolution.
func (o *overlay) resolve(id string) bool {
	if o.resolved[id] {
		return false
	}
	o.resolved[id] = true
	delete(o.judge, id)
	for i := range o.prompts {
		if o.prompts[i].ID == id {
			o.prompts = append(o.prompts[:i:i], o.prompts[i+1:]...)
			break
		}
	}
	return true
}

func (o *overlay) live(st convevents.SessionStatusEvent, content convevents.Inflight) convevents.LiveOverlay {
	ov := convevents.LiveOverlay{Status: st, Prompts: append([]convevents.Prompt{}, o.prompts...),
		Judge: []convevents.PromptJudge{}, Background: o.background, Queue: o.queue,
		Inflight: content, LiveConnection: o.liveConnect}
	for _, p := range o.prompts {
		if j, ok := o.judge[p.ID]; ok {
			ov.Judge = append(ov.Judge, j)
		}
	}
	return ov
}

// inflight is streamed content with the field values the deltas built, so a
// hello's parts line up with later delta offsets. Each streamed value is kept
// once, in its field, and stripped from the part data until it is rendered.
type inflight struct {
	msgOrder  []string
	msgs      map[string]db.Message
	partOrder []string
	parts     map[string]*livePart
}

type livePart struct {
	// part.Data holds no streamed field values; fields does (withFields).
	part   convevents.Part
	fields map[string]*streamField
	// final is set when the last upsert could no longer grow.
	final bool
}

// streamField appends deltas without copying the value built so far and
// keeps its UTF-16 length, the unit delta offsets use.
type streamField struct {
	b     strings.Builder
	units int
}

func (f *streamField) set(v string) {
	f.b.Reset()
	f.b.WriteString(v)
	f.units = convevents.UTF16Len(v)
}

func (lp *livePart) values() map[string]string {
	out := make(map[string]string, len(lp.fields))
	for k, f := range lp.fields {
		out[k] = f.b.String()
	}
	return out
}

// data renders the part with its streamed values.
func (lp *livePart) data() json.RawMessage { return withFields(lp.part.Data, lp.values()) }

func (lp *livePart) bytes() int {
	n := len(lp.part.Data)
	for _, f := range lp.fields {
		n += f.b.Len()
	}
	return n
}

func (f *inflight) message(m db.Message) {
	if f.msgs == nil {
		f.msgs = map[string]db.Message{}
	}
	if _, ok := f.msgs[m.ID]; !ok {
		f.msgOrder = append(f.msgOrder, m.ID)
	}
	f.msgs[m.ID] = m
}

func (f *inflight) upsert(p convevents.Part, final bool) {
	if f.parts == nil {
		f.parts = map[string]*livePart{}
	}
	lp := f.parts[p.ID]
	if lp == nil {
		lp = &livePart{fields: map[string]*streamField{}}
		f.parts[p.ID] = lp
		f.partOrder = append(f.partOrder, p.ID)
	}
	values := streamedFields(p.Data)
	p.Data = stripFields(p.Data, values)
	lp.part, lp.final = p, final
	if final {
		lp.fields = map[string]*streamField{}
	}
	for field, value := range values {
		cur := lp.fields[field]
		if cur == nil {
			cur = &streamField{}
			lp.fields[field] = cur
		} else if convevents.UTF16Len(value) < cur.units {
			continue
		}
		cur.set(value)
	}
}

// delta applies an offset-anchored append the way the client does: an
// overlap appends only the unseen tail and a gap is refused.
func (f *inflight) delta(d convevents.PartDelta) {
	lp := f.parts[d.PartID]
	if lp == nil {
		return
	}
	cur := lp.fields[d.Field]
	if cur == nil {
		cur = &streamField{}
		lp.fields[d.Field] = cur
	}
	switch {
	case d.Offset == cur.units:
		cur.b.WriteString(d.Text)
		cur.units += convevents.UTF16Len(d.Text)
	case d.Offset < cur.units:
		units := utf16.Encode([]rune(d.Text))
		if overlap := cur.units - d.Offset; overlap < len(units) {
			cur.b.WriteString(string(utf16.Decode(units[overlap:])))
			cur.units += len(units) - overlap
		}
	}
}

func (f *inflight) removeMessage(id string) {
	delete(f.msgs, id)
	f.msgOrder = without(f.msgOrder, id)
	for pid, lp := range f.parts {
		if lp.part.MessageID == id {
			f.removePart(pid)
		}
	}
}

func (f *inflight) removePart(id string) {
	delete(f.parts, id)
	f.partOrder = without(f.partOrder, id)
}

func without(ids []string, id string) []string {
	for i, v := range ids {
		if v == id {
			return append(ids[:i:i], ids[i+1:]...)
		}
	}
	return ids
}

func (f *inflight) snapshot() convevents.Inflight {
	out := convevents.Inflight{Messages: []db.Message{}, Parts: []convevents.Part{}}
	for _, id := range f.msgOrder {
		out.Messages = append(out.Messages, f.msgs[id])
	}
	for _, id := range f.partOrder {
		lp := f.parts[id]
		p := lp.part
		p.Data = lp.data()
		out.Parts = append(out.Parts, p)
	}
	return out
}

type partFields struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	State struct {
		Status string `json:"status"`
		Output any    `json:"output"`
		Raw    string `json:"raw"`
	} `json:"state"`
}

func readPart(data json.RawMessage) partFields {
	var p partFields
	_ = json.Unmarshal(data, &p)
	return p
}

func streamedFields(data json.RawMessage) map[string]string {
	p := readPart(data)
	out := map[string]string{convevents.FieldText: p.Text, convevents.FieldToolInput: p.State.Raw}
	if s, ok := p.State.Output.(string); ok {
		out[convevents.FieldToolOutput] = s
	}
	return out
}

// stripFields blanks the part data's streamed string values; the inflight
// fields hold them, so the text is not kept twice.
func stripFields(data json.RawMessage, values map[string]string) json.RawMessage {
	blank := map[string]bool{}
	for field, v := range values {
		if v != "" {
			blank[field] = true
		}
	}
	if len(blank) == 0 {
		return data
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return data
	}
	empty := json.RawMessage(`""`)
	if blank[convevents.FieldText] {
		obj["text"] = empty
	}
	if blank[convevents.FieldToolOutput] || blank[convevents.FieldToolInput] {
		var state map[string]json.RawMessage
		if json.Unmarshal(obj["state"], &state) == nil && state != nil {
			if blank[convevents.FieldToolOutput] {
				state["output"] = empty
			}
			if blank[convevents.FieldToolInput] {
				state["raw"] = empty
			}
			obj["state"], _ = json.Marshal(state)
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

// withFields writes streamed values longer than the snapshot's back into
// the part data.
func withFields(data json.RawMessage, fields map[string]string) json.RawMessage {
	have := streamedFields(data)
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return data
	}
	changed := false
	var state map[string]json.RawMessage
	for field, value := range fields {
		if convevents.UTF16Len(value) <= convevents.UTF16Len(have[field]) {
			continue
		}
		raw, _ := json.Marshal(value)
		if field == convevents.FieldText {
			obj["text"], changed = raw, true
			continue
		}
		if state == nil {
			_ = json.Unmarshal(obj["state"], &state)
			if state == nil {
				state = map[string]json.RawMessage{}
			}
		}
		key := "output"
		if field == convevents.FieldToolInput {
			key = "raw"
		}
		state[key], changed = raw, true
	}
	if !changed {
		return data
	}
	if state != nil {
		obj["state"], _ = json.Marshal(state)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}
