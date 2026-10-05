package ocv2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A fast reply can already be projected when a remote consumes step.started.
// Its streamed text must keep the same part ID as the stored reply.
func TestEventsStartedSnapshotDoesNotDuplicateReply(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1/message/msg01", http.StatusOK, `{"data":{"id":"msg01","type":"assistant",
		"time":{"created":100,"completed":104},"finish":"stop","content":[{"type":"text","text":"Hello!"}]}}`)
	const d = `"sessionID":"s1","assistantMessageID":"msg01"`
	f.events = []string{
		fakeEv("session.step.started", 100, "", `{`+d+`,"started":100}`),
		fakeEv("session.text.started", 101, "", `{`+d+`}`),
		fakeEv("session.text.delta", 102, "", `{`+d+`,"delta":"Hello!"}`),
		fakeEv("session.text.ended", 103, "", `{`+d+`,"text":"Hello!"}`),
		fakeEv("session.step.ended", 104, "", `{`+d+`,"finish":"stop"}`),
	}
	var reply string
	for _, ev := range cmpStream(t, f.URL+"/event") {
		p := cmpPart(ev)
		if p["type"] == "text" && p["id"] != PartID("msg01", 1) {
			t.Fatalf("reply emitted as extra text part %v, want stored part %s", p["id"], PartID("msg01", 1))
		}
		if p["type"] == "text" {
			reply = str(p, "text")
		}
	}
	if reply != "Hello!" {
		t.Fatalf("reply = %q, want Hello!", reply)
	}
}

// OpenCode updates time.created on retry without clearing the old content.
func TestEventsAheadRetrySnapshotKeepsEarlierParts(t *testing.T) {
	defer SetInstalledV2(true)()
	for _, tc := range []struct{ completed, refreshFails bool }{{}, {completed: true}, {refreshFails: true}} {
		t.Run(fmt.Sprintf("completed=%v/refreshFails=%v", tc.completed, tc.refreshFails), func(t *testing.T) {
			f := fakeServer(t)
			end := ""
			if tc.completed {
				end = `,"completed":9`
			}
			calls := 0
			f.on("GET /api/session/s1/message/msg01", func(fakeCall) (int, any) {
				calls++
				if tc.refreshFails && calls > 1 {
					return http.StatusServiceUnavailable, nil
				}
				return http.StatusOK, json.RawMessage(`{"data":{"id":"msg01","type":"assistant",
					"time":{"created":5` + end + `},"content":[{"type":"text","text":"first attempt"},{"type":"text","text":"retry reply"}]}}`)
			})
			const d = `"sessionID":"s1","assistantMessageID":"msg01"`
			f.events = []string{
				fakeEv("session.step.started", 5, "", `{`+d+`,"started":5}`),
				fakeEv("session.text.started", 6, "", `{`+d+`}`),
				fakeEv("session.text.delta", 7, "", `{`+d+`,"delta":"retry reply"}`),
				fakeEv("session.text.ended", 8, "", `{`+d+`,"text":"retry reply"}`),
				fakeEv("session.step.ended", 9, "", `{`+d+`,"finish":"stop"}`),
			}
			parts := map[string]string{}
			for _, ev := range cmpStream(t, f.URL+"/event") {
				if ev["type"] == "message.part.delta" {
					t.Fatal("buffered delta appended to an ahead-of-stream snapshot")
				}
				p := cmpPart(ev)
				if p["type"] == "text" {
					parts[str(p, "id")] = str(p, "text")
				}
			}
			if parts[PartID("msg01", 1)] != "first attempt" || parts[PartID("msg01", 2)] != "retry reply" || len(parts) != 2 {
				t.Fatalf("retry parts = %v, want earlier prefix at index 1 and reply at index 2", parts)
			}
		})
	}
}

func TestEventsLaterAttemptStreamsAfterAheadSnapshot(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1/message/msg01", http.StatusOK, `{"data":{"id":"msg01","type":"assistant",
		"time":{"created":5,"completed":9},"finish":"error","content":[{"type":"text","text":"first attempt"}]}}`)
	const d = `"sessionID":"s1","assistantMessageID":"msg01"`
	f.events = []string{
		fakeEv("session.step.started", 5, "", `{`+d+`,"started":5}`),
		fakeEv("session.step.failed", 9, "", `{`+d+`}`),
		fakeEv("session.step.started", 10, "", `{`+d+`,"started":10}`),
		fakeEv("session.text.started", 11, "", `{`+d+`}`),
		fakeEv("session.text.delta", 12, "", `{`+d+`,"delta":"retry"}`),
		fakeEv("session.text.ended", 13, "", `{`+d+`,"text":"retry"}`),
		fakeEv("session.step.ended", 14, "", `{`+d+`,"finish":"stop"}`),
	}
	streamed := false
	for _, ev := range cmpStream(t, f.URL+"/event") {
		if ev["type"] == "message.part.delta" {
			props := obj(ev, "properties")
			if props["partID"] != PartID("msg01", 2) || props["delta"] != "retry" {
				t.Fatalf("later attempt delta = %v", props)
			}
			streamed = true
		}
	}
	if !streamed {
		t.Fatal("later attempt did not resume delta streaming")
	}
}
