package protocol

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func noticeEvent(raw string) Event {
	return Event{Capability: NoticeCapability.Name, Name: NoticeSetEvent, Data: json.RawMessage(raw)}
}

func TestDecodeNoticeSnapshotAcceptsTheClosedSchema(t *testing.T) {
	for _, raw := range []string{
		`{"notices":[]}`,
		`{"notices":[{"id":"recording","tone":"warning","title":"Recording meeting"}]}`,
		`{"notices":[{"tone":"info","id":"a","title":"Ünïcödé ✓","body":"Body text.","startedAt":1763540000000,"actions":[{"actionId":"stop-recording"},{"actionId":"open"}]}]}`,
		`{"notices":[{"id":"a","tone":"info","title":"A"},{"id":"b","tone":"critical","title":"B"},{"id":"c","tone":"warning","title":"C"}]}`,
		`{"notices":[{"id":"a","tone":"info","title":"` + strings.Repeat("é", NoticeMaxTitle) + `"}]}`,
	} {
		if _, err := DecodeNoticeSnapshot(noticeEvent(raw)); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}

func TestDecodeNoticeSnapshotRejectsOpenOrUnsafePayloads(t *testing.T) {
	valid := `{"id":"a","tone":"info","title":"A"}`
	wrap := func(notice string) string { return `{"notices":[` + notice + `]}` }
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"notices":null}`, `{"Notices":[]}`, `{"notices":[],"notices":[]}`, `{"notices":[],"pluginId":"org.example.other"}`,
		wrap(`{"ID":"a","tone":"info","title":"A"}`),
		wrap(`{"id":"a","tone":"info","title":"A","url":"https://example.com"}`),
		wrap(`{"id":"a","tone":"info","title":"A","route":"/settings"}`),
		wrap(`{"id":"a","tone":"info","title":"A","body":""}`),
		wrap(`{"id":"a","tone":"info","title":"A","actions":[]}`),
		wrap(`{"id":"a","tone":"info","title":"A","actions":null}`),
		wrap(`{"id":"a","tone":"info","title":"A","startedAt":0}`),
		wrap(`{"id":"a","tone":"info","title":"A","startedAt":-1}`),
		wrap(`{"id":"a","tone":"info","title":"A","startedAt":1.5}`),
		wrap(`{"id":"a","tone":"info","title":"A","startedAt":"1"}`),
		wrap(`{"id":"a","tone":"loud","title":"A"}`),
		wrap(`{"id":"","tone":"info","title":"A"}`),
		wrap(`{"id":"Upper","tone":"info","title":"A"}`),
		wrap(`{"id":"` + strings.Repeat("a", 65) + `","tone":"info","title":"A"}`),
		wrap(`{"id":"a","tone":"info","title":""}`),
		wrap(`{"id":"a","tone":"info","title":" padded"}`),
		wrap(`{"id":"a","tone":"info","title":"` + strings.Repeat("x", NoticeMaxTitle+1) + `"}`),
		wrap(`{"id":"a","tone":"info","title":"A","body":"` + strings.Repeat("x", NoticeMaxBody+1) + `"}`),
		wrap(`{"id":"a","tone":"info","title":"<b>A</b>"}`),
		wrap(`{"id":"a","tone":"info","title":"A","body":"see https://example.com"}`),
		wrap(`{"id":"a","tone":"info","title":"A","body":"line\nbreak"}`),
		wrap(`{"id":"a","tone":"info","title":"A","actions":[{"actionId":"stop","label":"Delete everything"}]}`),
		wrap(`{"id":"a","tone":"info","title":"A","actions":[{"actionId":"stop"},{"actionId":"stop"}]}`),
		wrap(`{"id":"a","tone":"info","title":"A","actions":[{"actionId":"a"},{"actionId":"b"},{"actionId":"c"}]}`),
		wrap(`{"id":"a","tone":"info","title":"A","actions":[{"actionId":"Bad ID"}]}`),
		wrap(valid + `,` + valid),
		wrap(valid + `,{"id":"b","tone":"info","title":"B"},{"id":"c","tone":"info","title":"C"},{"id":"d","tone":"info","title":"D"}`),
		`{"notices":[]}` + strings.Repeat(" ", noticeMaxData),
	} {
		if _, err := DecodeNoticeSnapshot(noticeEvent(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := DecodeNoticeSnapshot(Event{Capability: NoticeCapability.Name, Name: "add", Data: json.RawMessage(`{"notices":[]}`)}); err == nil {
		t.Error("accepted the wrong event name")
	}
	if _, err := DecodeNoticeSnapshot(Event{Capability: NavBadgeCapability.Name, Name: NoticeSetEvent, Data: json.RawMessage(`{"notices":[]}`)}); err == nil {
		t.Error("accepted the wrong capability")
	}
}

func TestNoticeOptIn(t *testing.T) {
	d := *hello(ModeServe).Hello.Description
	d.Capabilities = append(d.Capabilities, NoticeCapability)
	if d.Validate() == nil {
		t.Fatal("declaration without requested grant accepted")
	}
	d.RequestedGrants = append(d.RequestedGrants, NoticeGrant)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	grant := []string{NoticeGrant}
	if d.NoticeAllowed(nil) || !d.NoticeAllowed(grant) {
		t.Fatal("notices did not require approval")
	}
	d.Capabilities = nil
	if d.NoticeAllowed(grant) {
		t.Fatal("grant without the capability was accepted")
	}
}

func TestProcessNoticesAreCopiedAndRequireAReadyChild(t *testing.T) {
	p := &Process{ctx: t.Context()}
	p.setNotices([]Notice{{ID: "a", Tone: NoticeToneInfo, Title: "A", Actions: []NoticeAction{{ActionID: "stop"}}}})
	if p.Notices() != nil {
		t.Fatal("notices exposed before the child was ready")
	}
	p.health.Status = "ready"
	got := p.Notices()
	got[0].Actions[0].ActionID = "mutated"
	if p.Notices()[0].Actions[0].ActionID != "stop" {
		t.Fatal("caller mutated the process snapshot")
	}
	p.setNotices(nil)
	if p.Notices() != nil {
		t.Fatal("cleared notices remain")
	}
}

// Every snapshot the field limits allow must fit the byte limit, even when a
// plugin escapes each character, or the host would keep stale notices.
func TestDecodeNoticeSnapshotAdmitsTheLargestValidSnapshot(t *testing.T) {
	// One emoji per rune, sent as a 12-byte escaped surrogate pair.
	escaped := func(n int) string { return strings.Repeat("\\ud83d\\ude00", n) }
	id := strings.Repeat("a", 64)
	action := func(c string) string { return `{"actionId":"` + strings.Repeat(c, 128) + `"}` }
	var notices []string
	for _, prefix := range []string{"a", "b", "c"} {
		notices = append(notices, `{"id":"`+prefix+id[1:]+`","tone":"critical","title":"`+escaped(NoticeMaxTitle)+`","body":"`+escaped(NoticeMaxBody)+
			`","startedAt":`+strconv.FormatInt(NoticeMaxStartedAt, 10)+`,"actions":[`+action("x")+`,`+action("y")+`]}`)
	}
	raw := `{"notices":[` + strings.Join(notices, ",") + `]}`
	if len(raw) < 12_000 {
		t.Fatalf("worst case is only %d bytes", len(raw))
	}
	if _, err := DecodeNoticeSnapshot(noticeEvent(raw)); err != nil {
		t.Fatalf("rejected a %d-byte valid snapshot: %v", len(raw), err)
	}
}

func TestNoticeStartedAtStaysRenderable(t *testing.T) {
	for at, ok := range map[int64]bool{1: true, NoticeMaxStartedAt: true, NoticeMaxStartedAt + 1: false, 8640000000000001: false} {
		raw := `{"notices":[{"id":"a","tone":"info","title":"A","startedAt":` + strconv.FormatInt(at, 10) + `}]}`
		if _, err := DecodeNoticeSnapshot(noticeEvent(raw)); (err == nil) != ok {
			t.Errorf("startedAt %d: err = %v, want accepted %v", at, err, ok)
		}
	}
}
