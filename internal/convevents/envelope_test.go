package convevents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func ptr[T any](v T) *T { return &v }

func samplePart() Part {
	return Part{Part: db.Part{ID: "prt1", MessageID: "msg1", SessionID: "ses1", TimeCreated: 5,
		Data: json.RawMessage(`{"type":"text","text":"hi"}`)}, Position: ptr(int64(2))}
}

// samples holds one populated value of every registered event type.
func samples() []Event {
	msg := db.Message{ID: "msg1", SessionID: "ses1", TimeCreated: 4, Data: json.RawMessage(`{"role":"assistant"}`)}
	notice := db.SessionNotice{Kind: "retry", Message: "rate limited", RetryAt: 9, Attempt: 2}
	status := SessionStatusEvent{Status: db.StatusBusy, Notice: Some(notice)}
	judge := PromptJudge{PromptID: "per1", Phase: JudgeScheduled, StartsAt: 7}
	prompt := Prompt{ID: "per1", Kind: PromptPermission, SessionID: "child", AskedAt: 3,
		Permission: &PermissionRequest{Permission: "bash", Patterns: []string{"ls"}, Always: []string{"ls *"}, Metadata: map[string]any{"command": "ls"}}}
	background := BackgroundChanged{Count: 1, Tasks: []BackgroundTask{{ID: "t1", Description: "sleep"}}}
	queue := QueueChanged{Messages: []QueuedMessage{{ID: "q1", Text: "next", Model: "m", CreatedAt: 1}}}
	return []Event{
		StreamHello{Protocol: 1, Revision: 3, Overlay: &LiveOverlay{
			Status: status, Prompts: []Prompt{prompt}, Judge: []PromptJudge{judge}, Background: background,
			Queue: &queue, Inflight: Inflight{Messages: []db.Message{msg}, Parts: []Part{samplePart()}}, LiveConnection: true,
		}},
		StreamHello{Protocol: 1, Resumed: true},
		StreamReset{Reason: ResetSlowConsumer},
		StreamError{Code: CodeOwnerOffline, Message: "gone", Retryable: true},
		MessageUpserted{Message: msg, Position: ptr(int64(1))},
		MessageRemoved{MessageID: "msg1", Reason: RemovedReverted},
		PartUpserted{Part: samplePart(), Final: true},
		PartRemoved{MessageID: "msg1", PartID: "prt1"},
		PartDelta{MessageID: "msg1", PartID: "prt1", Field: FieldToolOutput, Offset: 3, Text: "out", PartType: "tool"},
		status,
		SessionStatusEvent{Status: db.StatusDone, Provisional: true, Notice: Null[db.SessionNotice]()},
		SessionStatusEvent{Status: db.StatusLimited, ResetsAt: 99},
		TurnStarted{StartedAt: 1, MessageID: "msg1"},
		TurnEnded{Outcome: OutcomeError, EndedAt: 2, LatestMessageID: "msg1"},
		SessionUpdated{Patch: SessionPatch{Title: ptr("T"), SummaryAdditions: ptr(1), SummaryDeletions: ptr(2), SummaryFiles: ptr(3),
			ShareURL: ptr("https://x"), Archived: ptr(true), DefaultAgent: "build", DefaultModel: "p/m",
			Revert: Some(RevertMarker{MessageID: "msg1", PartID: "prt1"})}},
		SessionUpdated{Patch: SessionPatch{Revert: Null[RevertMarker]()}},
		HistoryRewritten{Reason: RewriteCompaction, FromMessageID: "msg1"},
		HistoryCommitted{Revision: 12},
		PromptAsked{Prompt: prompt},
		PromptAsked{Prompt: Prompt{ID: "que1", Kind: PromptQuestion, SessionID: "ses1", AskedAt: 1,
			Question: &QuestionRequest{Questions: []QuestionItem{{Question: "Q?", Header: "H",
				Options: []QuestionOption{{Label: "a", Description: "A"}}, Multiple: true, Custom: true}}}}},
		judge,
		PromptJudge{PromptID: "per1", Phase: JudgeFlagged, Reasoning: "risky"},
		PromptResolved{PromptID: "per1", Kind: PromptPermission, Outcome: ResolvedOnce, By: ByAI},
		queue,
		background,
		ChildUpserted{Child: Child{ID: "child", ParentID: "ses1", Title: "sub", TimeCreated: 1, TimeUpdated: 2, Agent: "explore", Deleted: true}},
		ChildStatus{ChildID: "child", Status: db.StatusWaiting, Notice: Null[db.SessionNotice]()},
		ChangesDirty{Reason: DirtyEdit},
		SessionError{MessageID: "msg1", Error: ErrorInfo{Name: "APIError", Message: "boom", StatusCode: 500}},
	}
}

func TestEveryTypeRoundTrips(t *testing.T) {
	seen := map[string]bool{}
	for i, ev := range samples() {
		seen[ev.EventType()] = true
		env := Envelope{Epoch: "e1", Seq: uint64(i + 1), SessionID: "ses1", Platform: "opencode", At: 1000, Replayed: i%2 == 0, Event: ev}
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("%s: marshal: %v", ev.EventType(), err)
		}
		var got Envelope
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("%s: unmarshal %s: %v", ev.EventType(), b, err)
		}
		want := env
		want.V, want.Durability = Version, ev.durability()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s round trip:\n got  %#v\n want %#v\n json %s", ev.EventType(), got, want, b)
		}
		again, _ := json.Marshal(got)
		if string(again) != string(b) {
			t.Fatalf("%s re-encode differs:\n%s\n%s", ev.EventType(), b, again)
		}
		if !strings.Contains(string(b), `"event":{"type":"`+ev.EventType()+`"`) {
			t.Fatalf("%s: discriminator not first: %s", ev.EventType(), b)
		}
	}
	if len(seen) != len(Types()) {
		t.Fatalf("samples cover %d of %d types", len(seen), len(Types()))
	}
}

func TestDurabilityClasses(t *testing.T) {
	want := map[string]Durability{
		TypeMessageUpserted: Durable, TypePartUpserted: Durable, TypeHistoryCommitted: Durable, TypeChildUpserted: Durable,
		TypePartDelta: Ephemeral, TypeSessionStatus: Live, TypeQueueChanged: Live, TypeStreamHello: Live,
	}
	for _, ev := range samples() {
		if d, ok := want[ev.EventType()]; ok && ev.durability() != d {
			t.Errorf("%s durability %s, want %s", ev.EventType(), ev.durability(), d)
		}
		if ev.durability() == "" {
			t.Errorf("%s has no durability", ev.EventType())
		}
	}
}

func TestNullableDistinguishesAbsentAndNull(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		set     bool
		cleared bool
	}{
		{`{"type":"session.status","status":"busy"}`, false, false},
		{`{"type":"session.status","status":"busy","notice":null}`, true, true},
		{`{"type":"session.status","status":"busy","notice":{"kind":"retry","message":"m","retryAt":1,"attempt":1}}`, true, false},
	} {
		ev, err := Decode([]byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		n := ev.(SessionStatusEvent).Notice
		if n.Set != tc.set || (n.Set && (n.Value == nil) != tc.cleared) {
			t.Errorf("%s: notice %+v", tc.raw, n)
		}
	}
	if b, _ := json.Marshal(SessionStatusEvent{Status: db.StatusBusy}); strings.Contains(string(b), "notice") {
		t.Errorf("absent notice marshalled: %s", b)
	}
	if err := json.Unmarshal([]byte(`{"kind":5}`), &Nullable[db.SessionNotice]{}); err == nil {
		t.Error("bad nullable value decoded")
	}
}

func TestDecodeUnknownAndMalformed(t *testing.T) {
	raw := `{"type":"future.thing","x":1}`
	ev, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	u, ok := ev.(Unknown)
	if !ok || u.EventType() != "future.thing" || u.durability() != "" {
		t.Fatalf("unknown = %#v", ev)
	}
	if b, _ := json.Marshal(u); string(b) != raw {
		t.Errorf("unknown re-encodes as %s", b)
	}
	if b, _ := json.Marshal(Unknown{Type: "x"}); string(b) != `{"type":"x"}` {
		t.Errorf("empty unknown = %s", b)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(`{"v":1,"epoch":"e","seq":1,"sessionId":"s","platform":"p","at":1,"durability":"live","event":`+raw+`}`), &env); err != nil {
		t.Fatalf("envelope with unknown event: %v", err)
	}
	for _, bad := range []string{`null`, `[1]`, `"x"`, `{"type":"part.delta","offset":"x"}`, `{`} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("Decode(%s) succeeded", bad)
		}
	}
	if err := json.Unmarshal([]byte(`{"event":null}`), &env); err == nil {
		t.Error("envelope without event decoded")
	}
	if err := json.Unmarshal([]byte(`{"seq":"x"}`), &env); err == nil {
		t.Error("malformed envelope decoded")
	}
	if _, err := json.Marshal(Envelope{}); err == nil {
		t.Error("envelope without event marshalled")
	}
}

func TestEnvelopeKeepsExplicitFields(t *testing.T) {
	b, err := json.Marshal(Envelope{V: 1, Durability: Durable, Event: Unknown{Type: "x"}})
	if err != nil || !strings.Contains(string(b), `"durability":"durable"`) {
		t.Fatalf("%s %v", b, err)
	}
}

func TestCursor(t *testing.T) {
	env := Envelope{Epoch: "owner:abc", Seq: 42}
	if env.Cursor() != "owner:abc:42" {
		t.Fatalf("cursor %q", env.Cursor())
	}
	epoch, seq, ok := ParseCursor(env.Cursor())
	if !ok || epoch != "owner:abc" || seq != 42 {
		t.Fatalf("parse = %q %d %v", epoch, seq, ok)
	}
	for _, bad := range []string{"", ":1", "e", "e:x", "e:-1"} {
		if _, _, ok := ParseCursor(bad); ok {
			t.Errorf("ParseCursor(%q) ok", bad)
		}
	}
}

func TestUTF16Len(t *testing.T) {
	for s, want := range map[string]int{"": 0, "abc": 3, "é": 1, "日本": 2, "😀": 2, "a😀b": 4, "\xff": 1} {
		if got := UTF16Len(s); got != want {
			t.Errorf("UTF16Len(%q) = %d, want %d", s, got, want)
		}
	}
}
