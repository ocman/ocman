package plugin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/sdk/protocol"
)

func TestNewNotices(t *testing.T) {
	recording := Notice{ID: "recording-7", Tone: NoticeToneWarning, Title: "Recording meeting", Body: "Audio is being captured.",
		StartedAt: 1763540000000, Actions: []NoticeAction{{ActionID: "stop-recording"}}}
	for _, notices := range [][]Notice{nil, {recording}, {{ID: "a", Tone: NoticeToneInfo, Title: "A"}, {ID: "b", Tone: NoticeToneCritical, Title: "B"}}} {
		event, err := NewNotices(notices...)
		if err != nil {
			t.Fatal(err)
		}
		got, err := protocol.DecodeNoticeSnapshot(event)
		if err != nil || len(got.Notices) != len(notices) || (len(notices) > 0 && !reflect.DeepEqual(got.Notices, notices)) {
			t.Fatalf("round trip = %+v, %v; want %+v", got, err, notices)
		}
	}
	for name, notices := range map[string][]Notice{
		"tone":      {{ID: "a", Tone: "loud", Title: "A"}},
		"title":     {{ID: "a", Tone: NoticeToneInfo, Title: strings.Repeat("x", NoticeMaxTitle+1)}},
		"too many":  {{ID: "a", Tone: NoticeToneInfo, Title: "A"}, {ID: "b", Tone: NoticeToneInfo, Title: "B"}, {ID: "c", Tone: NoticeToneInfo, Title: "C"}, {ID: "d", Tone: NoticeToneInfo, Title: "D"}},
		"duplicate": {{ID: "a", Tone: NoticeToneInfo, Title: "A"}, {ID: "a", Tone: NoticeToneInfo, Title: "B"}},
	} {
		if _, err := NewNotices(notices...); err == nil {
			t.Errorf("%s: accepted %+v", name, notices)
		}
	}
}
