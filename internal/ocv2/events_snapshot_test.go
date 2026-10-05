package ocv2

import (
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
