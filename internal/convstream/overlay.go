package convstream

import (
	"encoding/json"
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
	inflight    inflight
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

func (o *overlay) live(st convevents.SessionStatusEvent) convevents.LiveOverlay {
	ov := convevents.LiveOverlay{Status: st, Prompts: append([]convevents.Prompt{}, o.prompts...),
		Judge: []convevents.PromptJudge{}, Background: o.background, Queue: o.queue,
		Inflight: o.inflight.snapshot(), LiveConnection: o.liveConnect}
	for _, p := range o.prompts {
		if j, ok := o.judge[p.ID]; ok {
			ov.Judge = append(ov.Judge, j)
		}
	}
	return ov
}

// inflight is the current turn's content with the streamed field values
// the deltas built, so a hello's parts line up with later delta offsets.
type inflight struct {
	msgOrder  []string
	msgs      map[string]db.Message
	partOrder []string
	parts     map[string]*livePart
}

type livePart struct {
	part   convevents.Part
	fields map[string]string
}

func (f *inflight) reset() { *f = inflight{} }

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
		lp = &livePart{fields: map[string]string{}}
		f.parts[p.ID] = lp
		f.partOrder = append(f.partOrder, p.ID)
	}
	lp.part = p
	if final {
		lp.fields = streamedFields(p.Data)
		return
	}
	for field, value := range streamedFields(p.Data) {
		if convevents.UTF16Len(value) >= convevents.UTF16Len(lp.fields[field]) {
			lp.fields[field] = value
		}
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
	n := convevents.UTF16Len(cur)
	switch {
	case d.Offset == n:
		lp.fields[d.Field] = cur + d.Text
	case d.Offset < n:
		units := utf16.Encode([]rune(d.Text))
		if overlap := n - d.Offset; overlap < len(units) {
			lp.fields[d.Field] = cur + string(utf16.Decode(units[overlap:]))
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
		p.Data = withFields(p.Data, lp.fields)
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

// running reports a tool part still pending or running.
func running(data json.RawMessage) bool {
	p := readPart(data)
	return p.Type == "tool" && (p.State.Status == "running" || p.State.Status == "pending")
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
