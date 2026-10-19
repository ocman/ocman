package convstream

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const waitFor = 2 * time.Second

// fakeSource connects with seed, hands each connection's sink to the test
// and runs until the test ends the connection or the hub detaches.
type fakeSource struct {
	seed    Seed
	failErr error // returned before Connected
	sinks   chan Sink
	end     chan error

	mu      sync.Mutex
	watches int
	active  int

	lc      *platforms.SessionLifecycle
	lcErr   error
	block   chan struct{} // when set, Settle waits for it
	settled chan struct{}
}

func newFakeSource() *fakeSource {
	return &fakeSource{sinks: make(chan Sink, 8), end: make(chan error, 8), settled: make(chan struct{}, 16)}
}

func (f *fakeSource) Watch(ctx context.Context, sink Sink) error {
	f.mu.Lock()
	f.watches++
	f.active++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if f.failErr != nil {
		return f.failErr
	}
	sink.Connected(f.seed)
	f.sinks <- sink
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-f.end:
		return err
	}
}

func (f *fakeSource) Settle(ctx context.Context) (*platforms.SessionLifecycle, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	defer func() {
		select {
		case f.settled <- struct{}{}:
		default:
		}
	}()
	return f.lc, f.lcErr
}

func (f *fakeSource) counts() (watches, active int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watches, f.active
}

// bareSource cannot settle.
type bareSource struct{ f *fakeSource }

func (b bareSource) Watch(ctx context.Context, sink Sink) error { return b.f.Watch(ctx, sink) }

func newTestHub(t *testing.T, opts Options) *Hub {
	t.Helper()
	workers := &module.Workers{}
	opts.Workers = workers
	if opts.Backoff == 0 {
		opts.Backoff = time.Millisecond
	}
	h := NewHub(opts)
	t.Cleanup(func() {
		h.Close()
		workers.Close()
	})
	return h
}

var testKey = Key{Platform: "opencode", SessionID: "ses_root"}

func subscribe(t *testing.T, h *Hub, src Source, after string) *Subscription {
	t.Helper()
	sub, err := h.Subscribe(t.Context(), testKey, func() Source { return src }, after)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Close)
	return sub
}

func nextSink(t *testing.T, f *fakeSource) Sink {
	t.Helper()
	select {
	case s := <-f.sinks:
		return s
	case <-time.After(waitFor):
		t.Fatal("source never connected")
		return nil
	}
}

func decode(t *testing.T, frame []byte) convevents.Envelope {
	t.Helper()
	for _, line := range strings.Split(string(frame), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var env convevents.Envelope
			if err := json.Unmarshal([]byte(data), &env); err != nil {
				t.Fatalf("decode %q: %v", data, err)
			}
			return env
		}
	}
	t.Fatalf("frame without data: %q", frame)
	return convevents.Envelope{}
}

// drain returns the frames buffered for sub.
func drain(sub *Subscription) []frameMsg {
	var out []frameMsg
	for {
		select {
		case f := <-sub.C():
			out = append(out, f)
		default:
			return out
		}
	}
}

// await reads frames until one of type typ arrives.
func await(t *testing.T, sub *Subscription, typ string) []convevents.Envelope {
	t.Helper()
	var got []convevents.Envelope
	deadline := time.After(waitFor)
	for {
		select {
		case f := <-sub.C():
			env := decode(t, f.frame)
			got = append(got, env)
			if env.Event.EventType() == typ {
				return got
			}
		case <-deadline:
			t.Fatalf("no %s; got %v", typ, types(got))
			return nil
		}
	}
}

func types(envs []convevents.Envelope) []string {
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = e.Event.EventType()
		if st, ok := e.Event.(convevents.SessionStatusEvent); ok {
			out[i] += ":" + string(st.Status)
			if st.Provisional {
				out[i] += "?"
			}
		}
		if te, ok := e.Event.(convevents.TurnEnded); ok {
			out[i] += ":" + te.Outcome
		}
	}
	return out
}

func decodeAll(t *testing.T, frames []frameMsg) []convevents.Envelope {
	out := make([]convevents.Envelope, len(frames))
	for i, f := range frames {
		out[i] = decode(t, f.frame)
	}
	return out
}

func textPart(id, text string, final bool) convevents.PartUpserted {
	data, _ := json.Marshal(map[string]any{"type": "text", "text": text})
	return convevents.PartUpserted{Final: final, Part: convevents.Part{Part: db.Part{ID: id, MessageID: "msg_1", SessionID: testKey.SessionID, Data: data}}}
}

func toolPart(id, status string) convevents.PartUpserted {
	data, _ := json.Marshal(map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": status}})
	return convevents.PartUpserted{Part: convevents.Part{Part: db.Part{ID: id, MessageID: "msg_1", SessionID: testKey.SessionID, Data: data}}}
}

func delta(part string, offset int, text string) convevents.PartDelta {
	return convevents.PartDelta{MessageID: "msg_1", PartID: part, Field: convevents.FieldText, Offset: offset, Text: text, PartType: "text"}
}

func status(s db.SessionStatus) convevents.SessionStatusEvent {
	return convevents.SessionStatusEvent{Status: s, Notice: convevents.Null[db.SessionNotice]()}
}

func idle() convevents.SessionStatusEvent {
	return convevents.SessionStatusEvent{Status: db.StatusDone, Provisional: true, Notice: convevents.Null[db.SessionNotice]()}
}

func prompt(id, session string) convevents.PromptAsked {
	return convevents.PromptAsked{Prompt: convevents.Prompt{ID: id, Kind: convevents.PromptPermission, SessionID: session,
		Permission: &convevents.PermissionRequest{Permission: "bash", Patterns: []string{"ls"}}}}
}

var errBoom = errors.New("boom")
