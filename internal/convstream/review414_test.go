package convstream

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSnapshotRefreshDoesNotAnchorQueuedDeltas(t *testing.T) {
	props := func(text string) map[string]any {
		return map[string]any{"sessionID": testKey.SessionID, "messageID": "m", "partID": "p", "field": "text", "delta": text}
	}
	fp := &fakePlatform{detail: &platforms.SessionDetail{Parts: []db.Part{{ID: "p", MessageID: "m", Data: json.RawMessage(`{"type":"text","text":"abc"}`)}}}, frames: []string{
		v1("message.part.delta", props("a")), v1("message.part.delta", props("b")), v1("message.part.delta", props("c")),
		v1("message.part.updated", map[string]any{"part": map[string]any{"id": "p", "messageID": "m", "sessionID": testKey.SessionID, "type": "text", "text": "abc"}}),
		v1("message.part.delta", props("d")),
	}}
	src := &proxyLiveSource{key: testKey, adapter: fp, grace: time.Hour, proxy: func(ctx context.Context, id string, a platforms.Platform, raw, _ io.Writer, flush func()) error {
		return a.ProxyEvents(ctx, id, raw, flush)
	}}
	sink := &recordSink{}
	if err := src.Watch(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	var f inflight
	for _, ev := range sink.events {
		switch e := ev.(type) {
		case convevents.PartUpserted:
			f.upsert(e.Part, e.Final)
		case convevents.PartDelta:
			f.delta(e)
		}
	}
	if got := readPart(f.snapshot().Parts[0].Data).Text; got != "abcd" {
		t.Fatalf("snapshot-covered deltas duplicated: %q", got)
	}
}

func TestReconnectInvalidatesPreviousSettle(t *testing.T) {
	s := newSession(newTestHub(t, Options{}), testKey)
	s.connectLocked(Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}})
	// Model a settle blocked outside the session lock at the old idle edge.
	s.st.turn, s.st.pendingEnd = false, true
	s.st.gen++
	old := s.st.gen
	s.connectLocked(Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}})
	sub := s.attachLocked("")
	s.settledLocked(old, &platforms.SessionLifecycle{Status: db.StatusDone}, nil, false)
	if got := drain(sub); len(got) != 0 || s.st.pendingEnd || !s.st.turn {
		t.Fatalf("old settle crossed epoch: frames=%v state=%+v", got, s.st)
	}
}

func TestFinalOverlayDoesNotRestoreOmittedOutput(t *testing.T) {
	for _, output := range []string{"", `,"output":null`, `,"output":{"error":true}`} {
		var f inflight
		p := convevents.Part{Part: db.Part{ID: "p", MessageID: "m", Data: json.RawMessage(`{"type":"tool","state":{"status":"running","output":"old"}}`)}}
		f.upsert(p, false)
		p.Data = json.RawMessage(`{"type":"tool","state":{"status":"error"` + output + `}}`)
		f.upsert(p, true)
		if got := string(f.snapshot().Parts[0].Data); got != string(p.Data) {
			t.Fatalf("final replacement changed: %s, want %s", got, p.Data)
		}
	}
}
