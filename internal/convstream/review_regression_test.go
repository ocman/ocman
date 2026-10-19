package convstream

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestNumberedFramesDeliverEveryMappedEventAndDeduplicateBeforeMapping(t *testing.T) {
	part := map[string]any{"id": "edit", "messageID": "m", "sessionID": testKey.SessionID, "type": "tool", "tool": "edit", "state": map[string]any{"status": "completed", "output": "done"}}
	text := map[string]any{"id": "text", "messageID": "m", "sessionID": testKey.SessionID, "type": "text", "text": "a"}
	delta := "id: 3\n" + v1("message.part.delta", map[string]any{"sessionID": testKey.SessionID, "messageID": "m", "partID": "text", "delta": "b"})
	fp := &fakePlatform{id: "opencode", hold: true, frames: []string{
		"id: 1\n" + v1("message.part.updated", map[string]any{"part": part}),
		"id: 2\n" + v1("message.part.updated", map[string]any{"part": text}), delta, delta,
		"id: 4\n" + v1("message.part.delta", map[string]any{"sessionID": testKey.SessionID, "messageID": "m", "partID": "text", "delta": "c"}),
	}}
	src := &proxyLiveSource{key: testKey, adapter: fp, grace: time.Hour,
		proxy: func(ctx context.Context, id string, p platforms.Platform, raw, _ io.Writer, flush func()) error {
			return p.ProxyEvents(ctx, id, raw, flush)
		}}
	h := newTestHub(t, Options{})
	sub := subscribe(t, h, src, "")
	await(t, sub, convevents.TypeChangesDirty)
	var deltas []convevents.PartDelta
	for len(deltas) < 2 {
		for _, env := range await(t, sub, convevents.TypePartDelta) {
			if d, ok := env.Event.(convevents.PartDelta); ok {
				deltas = append(deltas, d)
			}
		}
	}
	if deltas[0].Offset != 1 || deltas[1].Offset != 2 || deltas[1].Text != "c" {
		t.Fatalf("duplicate frame changed mapper anchors: %+v", deltas)
	}
}

type blockedResponse struct{ net.Conn }

func (b blockedResponse) Header() http.Header { return http.Header{} }
func (b blockedResponse) WriteHeader(int)     {}
func (b blockedResponse) FlushError() error   { return nil }

func TestStreamWriteTimesOutWhenClientStopsReading(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	w := blockedResponse{server}
	o := sseOut{w: w, rc: http.NewResponseController(w)}
	done := make(chan bool, 1)
	go func() { done <- o.write([]byte("data: blocked\n\n")) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("non-reading client accepted write")
		}
	case <-time.After(7 * time.Second):
		server.Close()
		<-done
		t.Fatal("SSE write blocked without a deadline")
	}
}
