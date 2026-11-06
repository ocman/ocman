package convstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/convevents/v1map"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type displayRecordSink struct {
	*recordSink
	ready chan struct{}
	once  sync.Once
}

func TestLateDisplaySnapshotCannotOverwriteOrderedContent(t *testing.T) {
	for _, removal := range []bool{false, true} {
		t.Run(fmt.Sprint(removal), func(t *testing.T) {
			p := &blockedDetailPlatform{fakePlatform: &fakePlatform{detail: &platforms.SessionDetail{Parts: []db.Part{{ID: "p", MessageID: "m", Data: mustJSON(map[string]any{"id": "p", "messageID": "m", "sessionID": testKey.SessionID, "type": "text", "text": "old"})}}}}, started: make(chan struct{}), release: make(chan struct{})}
			ctx, cancel := context.WithCancel(t.Context())
			f := &feeder{p: &proxyLiveSource{key: testKey, adapter: p}, ctx: ctx, cancel: cancel, sink: &recordSink{}, mapper: v1map.New(v1map.Options{SessionID: testKey.SessionID}), connected: true}
			defer f.close()
			f.mu.Lock()
			f.refreshLocked(v1map.PartRef{MessageID: "m", PartID: "p"})
			f.mu.Unlock()
			<-p.started
			var frame string
			if removal {
				frame = v1("message.part.removed", map[string]any{"sessionID": testKey.SessionID, "messageID": "m", "partID": "p"})
			} else {
				frame = v1("message.part.updated", map[string]any{"part": map[string]any{"id": "p", "messageID": "m", "sessionID": testKey.SessionID, "type": "text", "text": "new"}})
			}
			if _, err := f.Write([]byte(frame)); err != nil {
				t.Fatal(err)
			}
			close(p.release)
			f.refresh.workers.Wait()
			events := f.sink.(*recordSink).events
			if len(events) != 1 {
				t.Fatalf("stale snapshot produced extra events: %v", len(events))
			}
			if !removal {
				part := events[0].(convevents.PartUpserted).Part
				if string(part.Data) == "" || !containsText(part.Data, "new") {
					t.Fatalf("new content overwritten: %s", part.Data)
				}
			}
		})
	}
}

func containsText(data []byte, want string) bool {
	var part struct {
		Text string `json:"text"`
	}
	return json.Unmarshal(data, &part) == nil && part.Text == want
}

func TestDisplayRefreshAdmissionIsBoundedAndCloseCancelsRead(t *testing.T) {
	p := &blockedDetailPlatform{fakePlatform: &fakePlatform{}, started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	f := &feeder{p: &proxyLiveSource{key: testKey, adapter: p}, ctx: ctx, cancel: cancel, sink: &recordSink{}}
	f.mu.Lock()
	f.refreshLocked(v1map.PartRef{MessageID: "m", PartID: "first"})
	f.mu.Unlock()
	<-p.started
	f.mu.Lock()
	for i := range 1000 {
		f.refreshLocked(v1map.PartRef{MessageID: "m", PartID: fmt.Sprint(i)})
	}
	if len(f.refresh.pending) != maxDisplayRefreshes || len(f.refresh.jobs) != maxDisplayRefreshes+1 {
		t.Errorf("unbounded refresh work: %d queued, %d retained", len(f.refresh.pending), len(f.refresh.jobs))
	}
	f.mu.Unlock()
	done := make(chan struct{})
	go func() { f.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(waitFor):
		t.Fatal("close did not cancel and join the snapshot reader")
	}
}

func TestDisplayRefreshPrunesCompletedDeduplication(t *testing.T) {
	f := &feeder{refresh: displayRefreshState{reading: true, jobs: map[v1map.PartRef]*displayRefresh{}}}
	for i := range 4096 {
		ref := v1map.PartRef{MessageID: "m", PartID: fmt.Sprint(i)}
		f.refresh.jobs[ref] = &displayRefresh{ref: ref, done: true}
	}
	ref := v1map.PartRef{MessageID: "m", PartID: "new"}
	f.refreshLocked(ref)
	f.refreshLocked(ref)
	if len(f.refresh.jobs) != 1 || len(f.refresh.pending) != 1 {
		t.Fatal("completed references retained or duplicate read admitted")
	}
	f.closed = true
	f.refreshLocked(v1map.PartRef{MessageID: "m", PartID: "after close"})
	if len(f.refresh.jobs) != 1 {
		t.Fatal("closed feeder admitted display work")
	}
}

func (s *displayRecordSink) Emit(ev convevents.Event, meta Meta) {
	s.recordSink.Emit(ev, meta)
	if _, ok := ev.(convevents.PartUpserted); ok {
		s.once.Do(func() { close(s.ready) })
	}
}

type blockedDetailPlatform struct {
	*fakePlatform
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockedDetailPlatform) Session(ctx context.Context, _ string, _, _ int) (*platforms.SessionDetail, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return p.detail, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDisplayRefreshDoesNotBlockSyntheticEvents(t *testing.T) {
	p := &blockedDetailPlatform{fakePlatform: &fakePlatform{id: "opencode", live: &platforms.LiveState{Status: "busy"}},
		started: make(chan struct{}), release: make(chan struct{})}
	src := &proxyLiveSource{key: testKey, adapter: p, grace: time.Hour,
		proxy: func(ctx context.Context, _ string, _ platforms.Platform, raw, synthetic io.Writer, _ func()) error {
			_, _ = io.WriteString(raw, v1("server.connected", map[string]any{}))
			rawDone := make(chan struct{})
			go func() {
				defer close(rawDone)
				_, _ = io.WriteString(raw, v1("message.part.delta", map[string]any{"sessionID": testKey.SessionID, "messageID": "m", "partID": "p", "field": "text", "delta": "x"}))
			}()
			select {
			case <-p.started:
			case <-ctx.Done():
				return ctx.Err()
			}
			written := make(chan struct{})
			go func() {
				defer close(written)
				_, _ = io.WriteString(synthetic, "event: ocman.permission.checking\ndata: {\"permissionId\":\"permission\"}\n\n")
			}()
			select {
			case <-written:
			case <-time.After(200 * time.Millisecond):
				t.Error("display-only read blocked an unrelated permission event")
			}
			close(p.release)
			<-rawDone
			<-written
			return nil
		}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := src.Watch(ctx, &recordSink{}); err != nil {
		t.Fatal(err)
	}
}
