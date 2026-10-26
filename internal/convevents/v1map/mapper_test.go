package v1map

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
)

func frame(data string) Frame { return Frame{Data: json.RawMessage(data)} }

func newTestMapper(opts Options) *Mapper {
	if opts.SessionID == "" {
		opts.SessionID = "s"
	}
	if opts.Now == nil {
		opts.Now = func() int64 { return 42 }
	}
	return New(opts)
}

func types(evs []convevents.Event) []string {
	out := []string{}
	for _, ev := range evs {
		out = append(out, ev.EventType())
	}
	return out
}

func mapAll(m *Mapper, frames ...string) []convevents.Event {
	var out []convevents.Event
	for _, f := range frames {
		out = append(out, m.Map(frame(f)).Events...)
	}
	return out
}

func TestParseFrame(t *testing.T) {
	f := ParseFrame([]byte("id: 7\r\nevent: ocman.permission.pending\n: comment\ndata: {\"a\":\ndata: 1}\nretry: 5"))
	if f.ID != "7" || f.Event != "ocman.permission.pending" || string(f.Data) != "{\"a\":\n1}" {
		t.Fatalf("frame = %+v (%s)", f, f.Data)
	}
	comment := ParseFrame([]byte(": heartbeat"))
	if comment.Data != nil {
		t.Fatalf("comment frame has data %s", comment.Data)
	}
	r := newTestMapper(Options{}).Map(comment)
	if !r.Ignored {
		t.Fatalf("empty frame result %+v", r)
	}
}

func TestResultSideSignals(t *testing.T) {
	m := newTestMapper(Options{})
	r := m.Map(Frame{ID: "12", Data: json.RawMessage(`{"type":"ocman.queue.changed","properties":{"sessionID":"s"},"acknowledged":true}`)})
	if !r.QueueChanged || !r.Replayed || r.Ignored || len(r.Events) != 0 {
		t.Fatalf("queue result %+v", r)
	}
	r = m.Map(frame(`{"type":"message.part.delta","properties":{"messageID":"m","partID":"p","delta":"x"}}`))
	if want := []PartRef{{MessageID: "m", PartID: "p"}}; !reflect.DeepEqual(r.Unanchored, want) || len(r.Events) != 0 {
		t.Fatalf("unanchored result %+v", r)
	}
	if r := m.Map(frame(`{"type":"nope","properties":{}}`)); !r.Ignored {
		t.Fatal("unknown type not ignored")
	}
	if r := m.Map(frame(`{"type":"server.heartbeat"}`)); r.Ignored || len(r.Events) != 0 {
		t.Fatalf("heartbeat result %+v", r)
	}
}

func TestDeltaOffsetsFollowSnapshots(t *testing.T) {
	m := newTestMapper(Options{})
	part := func(text string, end bool) string {
		tm := `{"start":1}`
		if end {
			tm = `{"start":1,"end":2}`
		}
		return `{"type":"message.part.updated","properties":{"part":{"id":"p","messageID":"m","type":"text","text":"` + text + `","time":` + tm + `}}}`
	}
	delta := func(text string) string {
		return `{"type":"message.part.delta","properties":{"messageID":"m","partID":"p","field":"text","delta":"` + text + `"}}`
	}
	evs := mapAll(m, part("", false), delta("ab"), part("a", false), delta("😀"), part("xy", true), delta("z"),
		`{"type":"message.removed","properties":{"messageID":"m"}}`, delta("q"))
	var offsets []int
	for _, ev := range evs {
		if d, ok := ev.(convevents.PartDelta); ok {
			offsets = append(offsets, d.Offset)
		}
	}
	// A shorter non-final snapshot keeps the streamed "ab"; a final one
	// replaces it; a removed message unanchors its parts.
	if want := []int{0, 2, 2}; !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets %v, want %v (%v)", offsets, want, types(evs))
	}
}

func TestDeltaFiltering(t *testing.T) {
	m := newTestMapper(Options{})
	mapAll(m, `{"type":"message.part.updated","properties":{"part":{"id":"p","messageID":"m","type":"tool","state":{"status":"running","output":"x"}}}}`)
	for _, f := range []string{
		`{"type":"message.part.delta","properties":{"messageID":"m","partID":"p","field":"state.metadata","delta":"x"}}`,
		`{"type":"message.part.delta","properties":{"messageID":"m","partID":"p","field":"text","delta":""}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"other","messageID":"m","partID":"p","delta":"x"}}`,
	} {
		if evs := mapAll(m, f); len(evs) != 0 {
			t.Errorf("%s mapped to %v", f, types(evs))
		}
	}
	evs := mapAll(m, `{"type":"message.part.delta","properties":{"messageID":"m","partID":"p","field":"state.output","delta":"y"}}`)
	if d := evs[0].(convevents.PartDelta); d.Field != convevents.FieldToolOutput || d.Offset != 1 || d.PartType != "tool" {
		t.Fatalf("tool delta %+v", d)
	}
}

func TestStatusEdges(t *testing.T) {
	m := newTestMapper(Options{})
	st := func(kind string) string {
		return `{"type":"session.status","properties":{"sessionID":"s","status":{"type":"` + kind + `"}}}`
	}
	evs := mapAll(m,
		st("idle"), `{"type":"session.idle","properties":{"sessionID":"s"}}`, // twin
		`{"type":"message.created","properties":{"info":{"id":"m","role":"user","time":{"created":1}}}}`,
		`{"type":"session.idle"}`, // activity in between: a new edge
		`{"type":"session.status","properties":{"status":"waiting"}}`,
		st("interrupted"), st("bogus"), st("busy"), st("limited"),
		`{"type":"session.idle"}`, // withheld while limited
		st("idle"),                // a status idle still settles

	)
	var got []string
	for _, ev := range evs {
		if s, ok := ev.(convevents.SessionStatusEvent); ok {
			got = append(got, string(s.Status)+map[bool]string{true: "?"}[s.Provisional])
		}
	}
	if want := []string{"done?", "done?", "waiting", "interrupted", "busy", "limited", "done?"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses %v, want %v", got, want)
	}
	if m := (evs[1].(convevents.MessageUpserted)).Message; m.SessionID != "s" || string(m.Data) != `{"role":"user","time":{"created":1}}` {
		t.Fatalf("message %+v %s", m, m.Data)
	}
}

func TestDescendantOption(t *testing.T) {
	m := newTestMapper(Options{Descendant: func(id string) bool { return id == "known" }})
	evs := mapAll(m,
		`{"type":"session.status","properties":{"sessionID":"known","status":{"type":"retry","attempt":1,"message":"m","next":5}}}`,
		`{"type":"session.status","properties":{"sessionID":"known","status":{"type":"bogus"}}}`,
		`{"type":"session.idle","properties":{"sessionID":"stranger"}}`,
		`{"type":"session.updated","properties":{"info":{"id":"known","title":"t"}}}`,
		`{"type":"session.created","properties":{"info":{"id":"grandchild","parentID":"known"}}}`,
		`{"type":"session.created","properties":{"info":{"id":"stranger","parentID":"elsewhere"}}}`,
		`{"type":"session.deleted","properties":{"info":{"id":"stranger"}}}`,
		`{"type":"session.updated","properties":{"sessionID":""}}`,
		`{"type":"session.created","properties":{"info":{"id":"s"}}}`,
	)
	if got := strings.Join(types(evs), ","); got != "child.status,child.upserted,child.upserted" {
		t.Fatalf("events %s", got)
	}
	if n := evs[0].(convevents.ChildStatus).Notice; n.Value == nil || n.Value.Attempt != 1 || n.Value.RetryAt != 5 {
		t.Fatalf("child retry notice %+v", n)
	}
}

func TestPromptEdgeCases(t *testing.T) {
	m := newTestMapper(Options{})
	evs := mapAll(m,
		`{"type":"permission.asked","properties":{"requestID":"p1"}}`,
		`{"type":"permission.asked","properties":{"sessionID":"s"}}`,
		`{"type":"permission.replied","properties":{"requestID":"p1","reply":"weird"}}`,
		`{"type":"permission.replied","properties":{}}`,
		`{"type":"question.replied","properties":{}}`,
		`{"type":"question.asked","properties":{"id":"q","questions":{"questions":[{"question":"Q","options":[{"label":"a"}],"multiple":true}]}}}`,
		`{"type":"question.asked","properties":{"id":"q2","questions":[{"question":5,"options":[]}]}}`,
		`{"event":"x"}`,
		`{"type":"ocman.permission.auto-approved","properties":{"permissionId":"p1"}}`,
		`{"type":"ocman.permission.auto-approved","properties":{"permissionId":"p3","permission":"read","reply":"always"}}`,
	)
	if got := strings.Join(types(evs), ","); got != "prompt.asked,prompt.resolved,prompt.asked,message.upserted,part.upserted,prompt.resolved" {
		t.Fatalf("events %s", got)
	}
	ask := evs[0].(convevents.PromptAsked).Prompt
	if ask.SessionID != "s" || ask.AskedAt != 42 || ask.Permission.Permission != "Permission required" || ask.Permission.Patterns == nil {
		t.Fatalf("prompt %+v", ask)
	}
	if o := evs[1].(convevents.PromptResolved).Outcome; o != convevents.ResolvedDismissed {
		t.Fatalf("outcome %s", o)
	}
	if q := evs[2].(convevents.PromptAsked).Prompt.Question.Questions[0]; !q.Multiple || q.Options[0].Label != "a" {
		t.Fatalf("question %+v", q)
	}
	notice := evs[4].(convevents.PartUpserted).Part
	if notice.TimeCreated != 42 || !strings.Contains(string(notice.Data), `"approvedBy":"ai"`) || !strings.Contains(string(notice.Data), `"metadata":null`) {
		t.Fatalf("notice %+v %s", notice, notice.Data)
	}
	if r := evs[5].(convevents.PromptResolved); r.Outcome != convevents.ResolvedAlways || r.By != convevents.ByAI {
		t.Fatalf("resolution %+v", r)
	}
}

func TestSessionErrorShapes(t *testing.T) {
	m := newTestMapper(Options{})
	evs := mapAll(m,
		`{"type":"session.error","properties":{"error":{"name":"ProviderAuthError","message":"bad key"}}}`,
		`{"type":"session.error","properties":{"error":{"name":"MessageAbortedError"}}}`,
		`{"type":"session.error","properties":{"sessionID":"other","error":{"name":"x"}}}`,
		`{"type":"session.error","properties":{"sessionID":"s"}}`,
		`{"type":"session.compacted","properties":{"sessionID":"other"}}`,
		`{"type":"session.diff","properties":{"sessionID":"other"}}`,
		`{"type":"message.removed","properties":{"sessionID":"other","messageID":"m"}}`,
		`{"type":"message.part.removed","properties":{"messageID":"m"}}`,
		`{"type":"message.updated","properties":{"info":{"id":"m","sessionID":"other"}}}`,
		`{"type":"message.part.updated","properties":{"part":{"id":"p","messageID":"m","type":"step-finish"}}}`,
	)
	if len(evs) != 2 {
		t.Fatalf("events %v", types(evs))
	}
	if e := evs[0].(convevents.SessionError).Error; e.Message != "bad key" || e.Name != "ProviderAuthError" {
		t.Fatalf("error %+v", e)
	}
	if e := evs[1].(convevents.SessionError).Error; e.Message != "MessageAbortedError" {
		t.Fatalf("error %+v", e)
	}
}

func TestNormalizeChannels(t *testing.T) {
	m := newTestMapper(Options{})
	for _, tc := range []struct {
		f    Frame
		want string
	}{
		{Frame{Event: "message", Data: json.RawMessage(`{"type":"session.diff","properties":{}}`)}, "changes.dirty"},
		{Frame{Event: "permission.asked", Data: json.RawMessage(`{"type":"permission.asked","properties":{"id":"x"}}`)}, "prompt.asked"},
		{Frame{Event: "message.part.updated", Data: json.RawMessage(`{"id":"p","messageID":"m","type":"agent"}`)}, "part.upserted"},
		{Frame{Data: json.RawMessage(`{"directory":"/d","payload":{"sessionID":"s"},"type":"session.diff"}`)}, "changes.dirty"},
		{Frame{Data: json.RawMessage(`{"type":"x","info":{"id":"m"}}`)}, "message.upserted"},
		{Frame{Data: json.RawMessage(`{"type":"x","part":{"id":"p","messageID":"m"}}`)}, "part.upserted"},
	} {
		evs := m.Map(tc.f).Events
		if len(evs) == 0 || evs[0].EventType() != tc.want {
			t.Errorf("%s %s -> %v, want %s", tc.f.Event, tc.f.Data, types(evs), tc.want)
		}
	}
	if u := m.Map(frame(`{"type":"x","properties":{"info":{"id":"m"}}}`)).Events[0].(convevents.MessageUpserted); u.Message.TimeCreated != 42 {
		t.Fatalf("default creation time %d", u.Message.TimeCreated)
	}
}

func TestDefaultClock(t *testing.T) {
	m := New(Options{SessionID: "s"})
	ev := m.Map(frame(`{"type":"permission.asked","properties":{"id":"p"}}`)).Events[0].(convevents.PromptAsked)
	if ev.Prompt.AskedAt <= 0 {
		t.Fatalf("askedAt %d", ev.Prompt.AskedAt)
	}
	if s := (convevents.SessionStatusEvent{Status: db.StatusBusy}); s.Notice.Set {
		t.Fatal("zero notice is set")
	}
}

func TestHistoryCommittedNamesOnlyThePageSession(t *testing.T) {
	m := newTestMapper(Options{})
	got := mapAll(m,
		`{"type":"ocman.history.committed","properties":{"sessionID":"s","revision":7}}`,
		`{"type":"ocman.history.committed","properties":{"sessionID":"child","revision":8}}`,
		`{"type":"ocman.history.committed","properties":{"revision":9}}`,
		`{"type":"ocman.history.committed","properties":{"sessionID":"s","revision":0}}`,
		`{"type":"ocman.history.committed","properties":{"sessionID":"s","revision":1.5}}`,
	)
	if want := []convevents.Event{convevents.HistoryCommitted{Revision: 7}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}
