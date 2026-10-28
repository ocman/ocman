package convstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// fakePlatform implements the Platform methods the live source uses; any
// other call panics on the nil embedded interface.
type fakePlatform struct {
	platforms.Platform
	id        platforms.ID
	frames    []string
	hold      bool // keep the stream open after frames
	err       error
	live      *platforms.LiveState
	perms     []platforms.LivePrompt
	detail    *platforms.SessionDetail
	lifecycle *platforms.SessionLifecycle
}

func (f *fakePlatform) ID() platforms.ID { return f.id }

func (f *fakePlatform) ProxyEvents(ctx context.Context, _ string, w io.Writer, flush func()) error {
	if f.err != nil {
		return f.err
	}
	for _, fr := range f.frames {
		// Split mid-frame: only whole frames may reach the mapper.
		mid := len(fr) / 2
		if _, err := io.WriteString(w, fr[:mid]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, fr[mid:]); err != nil {
			return err
		}
	}
	if f.hold {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakePlatform) LiveStatus(string) *platforms.LiveState { return f.live }

func (f *fakePlatform) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return f.perms, nil
}

func (f *fakePlatform) ListQuestions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, platforms.ErrUnsupported
}

func (f *fakePlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	if f.detail == nil {
		return nil, platforms.ErrNotFound
	}
	return f.detail, nil
}

type lifecyclePlatform struct{ *fakePlatform }

func (l lifecyclePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return l.lifecycle, nil
}

func v1(event string, props any) string {
	data, _ := json.Marshal(map[string]any{"type": event, "properties": props})
	return fmt.Sprintf("data: %s\n\n", data)
}

// recordSink collects what a source delivers.
type recordSink struct {
	seed   *Seed
	events []convevents.Event
	queue  int
}

func (r *recordSink) Connected(s Seed)                 { r.seed = &s }
func (r *recordSink) Emit(ev convevents.Event, _ Meta) { r.events = append(r.events, ev) }
func (r *recordSink) QueueChanged()                    { r.queue++ }
func (r *recordSink) types() (out []string) {
	for _, ev := range r.events {
		out = append(out, ev.EventType())
	}
	return out
}

func TestProxySourceMapsFramesAndSynthetics(t *testing.T) {
	part := map[string]any{"id": "prt_1", "messageID": "msg_1", "sessionID": testKey.SessionID, "type": "text", "text": "Hello"}
	fp := &fakePlatform{id: "opencode", live: &platforms.LiveState{Status: "busy"},
		perms: []platforms.LivePrompt{{"id": "per_1", "sessionID": testKey.SessionID, "permission": "bash", "patterns": []any{"ls"}}},
		frames: []string{
			v1("server.connected", map[string]any{}),
			v1("message.part.delta", map[string]any{"sessionID": testKey.SessionID, "messageID": "msg_1", "partID": "prt_1", "field": "text", "delta": "lo"}),
			v1("message.part.delta", map[string]any{"sessionID": testKey.SessionID, "messageID": "msg_1", "partID": "prt_1", "field": "text", "delta": "!"}),
			v1("ocman.queue.changed", map[string]any{"sessionID": testKey.SessionID}),
		},
		detail: &platforms.SessionDetail{Parts: []db.Part{{ID: "prt_1", MessageID: "msg_1", Data: mustJSON(part)}}},
	}
	dir := t.TempDir()
	src := &proxyLiveSource{key: testKey, adapter: fp, grace: time.Hour, captureDir: dir,
		proxy: func(ctx context.Context, sid string, a platforms.Platform, raw, synthetic io.Writer, flush func()) error {
			_, _ = io.WriteString(synthetic, "event: ocman.permission.checking\ndata: {\"permissionId\":\"per_1\"}\n\n")
			return a.ProxyEvents(ctx, sid, raw, flush)
		}}
	sink := &recordSink{}
	if err := src.Watch(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	if sink.seed == nil || sink.seed.Status.Status != db.StatusBusy || len(sink.seed.Prompts) != 1 || sink.seed.Prompts[0].ID != "per_1" {
		t.Fatalf("seed = %+v", sink.seed)
	}
	// A cursorless refresh is display-only; queued unpositioned deltas wait
	// for an ordered upstream upsert rather than duplicating snapshot text.
	want := []string{"prompt.judge", "background.changed", "part.upserted"}
	if got := sink.types(); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if sink.queue != 1 {
		t.Fatalf("queue changes = %d", sink.queue)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "opencode-ses_root-*.v1.jsonl"))
	if len(files) != 1 {
		t.Fatalf("capture files = %v", files)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	for sc := bufio.NewScanner(f); sc.Scan(); {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 6 || lines[0] != `{"session":"ses_root"}` || !strings.Contains(lines[1], `"event":"ocman.permission.checking"`) {
		t.Fatalf("capture = %v", lines)
	}
}

func TestProxySourceSeedsBackgroundFromFirstFrame(t *testing.T) {
	fp := &fakePlatform{id: "opencode", frames: []string{
		v1("server.connected", map[string]any{
			"backgroundTasks":       map[string]any{testKey.SessionID: 1},
			"backgroundTaskDetails": map[string]any{testKey.SessionID: []any{map[string]any{"id": "sh1", "description": "npm run dev"}}},
		}),
	}}
	src := &proxyLiveSource{key: testKey, adapter: fp, grace: time.Hour,
		proxy: func(ctx context.Context, sid string, a platforms.Platform, raw, _ io.Writer, flush func()) error {
			return a.ProxyEvents(ctx, sid, raw, flush)
		}}
	sink := &recordSink{}
	if err := src.Watch(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	if bg := sink.seed.Background; bg == nil || bg.Count != 1 || bg.Tasks[0].ID != "sh1" || len(sink.events) != 0 {
		t.Fatalf("seed background = %+v, events = %v", bg, sink.types())
	}

	h := newTestHub(t, Options{})
	hubSrc := newFakeSource()
	hubSrc.seed = Seed{Background: sink.seed.Background}
	ov := decode(t, subscribe(t, h, hubSrc, "").Hello.frame).Event.(convevents.StreamHello).Overlay
	if ov.Background.Count != 1 {
		t.Fatalf("overlay background = %+v", ov.Background)
	}
}

func TestProxySourceSeedsStatusAndConnectsAfterGrace(t *testing.T) {
	fp := &fakePlatform{id: "opencode", hold: true, lifecycle: &platforms.SessionLifecycle{Status: db.StatusError}}
	src := &proxyLiveSource{key: testKey, adapter: lifecyclePlatform{fp}, grace: time.Millisecond,
		proxy: func(ctx context.Context, sid string, a platforms.Platform, raw, _ io.Writer, flush func()) error {
			return a.ProxyEvents(ctx, sid, raw, flush)
		}}
	h := newTestHub(t, Options{})
	sub, err := h.Subscribe(t.Context(), testKey, func() Source { return src }, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if st := decode(t, sub.Hello.frame).Event.(convevents.StreamHello).Overlay.Status; st.Status != db.StatusError || st.Provisional {
		t.Fatalf("seeded status = %+v", st)
	}
	if lc, err := src.Settle(t.Context()); err != nil || lc.Status != db.StatusError {
		t.Fatalf("Settle = %+v, %v", lc, err)
	}
	if _, err := (&proxyLiveSource{adapter: fp}).Settle(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("Settle without a lifecycle reader = %v", err)
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// The platform and auto-approve sinks write from different goroutines; each
// frame must reach the mapper whole and exactly once. Run under -race.
func TestProxySourceSerialisesConcurrentRawAndSyntheticFrames(t *testing.T) {
	const n = 200
	src := &proxyLiveSource{key: testKey, adapter: &fakePlatform{id: "opencode"}, grace: time.Hour,
		proxy: func(_ context.Context, _ string, _ platforms.Platform, raw, synthetic io.Writer, _ func()) error {
			done := make(chan struct{})
			go func() {
				defer close(done)
				for i := range n {
					frame := fmt.Sprintf("event: ocman.permission.checking\ndata: {\"permissionId\":\"per_%d\"}\n\n", i)
					_, _ = io.WriteString(synthetic, frame[:10])
					_, _ = io.WriteString(synthetic, frame[10:])
				}
			}()
			for range n {
				frame := v1("ocman.queue.changed", map[string]any{"sessionID": testKey.SessionID})
				_, _ = io.WriteString(raw, frame[:7])
				_, _ = io.WriteString(raw, frame[7:])
			}
			<-done
			return nil
		}}
	sink := &recordSink{}
	if err := src.Watch(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	judged := 0
	for _, typ := range sink.types() {
		if typ == convevents.TypePromptJudge {
			judged++
		}
	}
	if sink.queue != n || judged != n {
		t.Fatalf("queue changes = %d, judge phases = %d, want %d each", sink.queue, judged, n)
	}
}
