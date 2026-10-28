package convstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/convevents/v1map"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

// ProxyFunc runs a session's v1 byte stream: platform frames to raw and
// ocman's synthetic frames (auto-approve sinks) to synthetic, until the
// upstream ends. Owners wrap Platform.ProxyEvents with their tee here.
type ProxyFunc func(ctx context.Context, sessionID string, adapter platforms.Platform, raw, synthetic io.Writer, flush func()) error

const (
	defaultConnectGrace = time.Second
	bootstrapTimeout    = 5 * time.Second
	refreshTimeout      = 3 * time.Second
)

// proxyLiveSource adapts any Platform to a Source during migration: it runs
// the v1 stream through v1map (design §6.2). A connection counts as
// established at its first frame, or once it has stayed up for grace.
type proxyLiveSource struct {
	key        Key
	adapter    platforms.Platform
	proxy      ProxyFunc
	descendant func(string) bool
	grace      time.Duration
	captureDir string
	now        func() int64
}

func (p *proxyLiveSource) Settle(ctx context.Context) (*platforms.SessionLifecycle, error) {
	lr, ok := p.adapter.(platforms.LifecycleReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	return lr.SessionLifecycle(ctx, p.key.SessionID)
}

func (p *proxyLiveSource) Watch(ctx context.Context, sink Sink) error {
	f := &feeder{p: p, ctx: ctx, sink: sink, refreshed: map[string]bool{},
		mapper: v1map.New(v1map.Options{SessionID: p.key.SessionID, Now: p.now, Descendant: p.descendant})}
	f.capture = openCapture(p.captureDir, p.key)
	defer f.close()
	timer := time.AfterFunc(p.grace, f.connect)
	defer timer.Stop()
	raw := &httpx.SSEFrameWriter{Dst: f}
	synthetic := &httpx.SSEFrameWriter{Dst: f}
	return p.proxy(ctx, p.key.SessionID, p.adapter, raw, synthetic, func() {})
}

// feeder receives whole SSE frames from the platform and from synthetic
// writers, one at a time, and maps them in order.
type feeder struct {
	p    *proxyLiveSource
	ctx  context.Context
	sink Sink

	mu        sync.Mutex
	mapper    *v1map.Mapper
	connected bool
	closed    bool
	refreshed map[string]bool
	capture   *capture
	lastSeq   uint64
}

func (f *feeder) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return len(b), nil
	}
	frame := v1map.ParseFrame(bytes.TrimRight(b, "\r\n"))
	if len(frame.Data) == 0 {
		return len(b), nil
	}
	f.capture.write(frame)
	if seq, err := strconv.ParseUint(frame.ID, 10, 64); err == nil && seq != 0 {
		if seq <= f.lastSeq {
			return len(b), nil
		}
		f.lastSeq = seq
	}
	res := f.mapper.Map(frame)
	if !f.connected {
		// The first frame's background (server.connected) seeds the hello
		// overlay rather than following it.
		var seedBg *convevents.BackgroundChanged
		kept := res.Events[:0:0]
		for _, ev := range res.Events {
			if bg, ok := ev.(convevents.BackgroundChanged); ok {
				seedBg = &bg
				continue
			}
			kept = append(kept, ev)
		}
		res.Events = kept
		f.connectLocked(seedBg)
	}
	f.deliverLocked(res)
	return len(b), nil
}

func (f *feeder) connect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.connectLocked(nil)
	}
}

func (f *feeder) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.capture.close()
}

// connectLocked seeds live state once the upstream is up: the adapter's
// status, else its settled lifecycle, and the pending prompts mapped as
// the stream would carry them.
func (f *feeder) connectLocked(background *convevents.BackgroundChanged) {
	if f.connected {
		return
	}
	f.connected = true
	ctx, cancel := context.WithTimeout(f.ctx, bootstrapTimeout)
	defer cancel()
	seed := Seed{Status: f.seedStatus(ctx), Background: background}
	for _, list := range []struct {
		typ  string
		read func(context.Context, string) ([]platforms.LivePrompt, error)
	}{{"permission.asked", f.p.adapter.ListPermissions}, {"question.asked", f.p.adapter.ListQuestions}} {
		prompts, err := list.read(ctx, f.p.key.SessionID)
		if err != nil {
			log.WithError(err).WithField("sessionID", f.p.key.SessionID).Debug("convstream: listing pending prompts")
			continue
		}
		for _, lp := range prompts {
			data, err := json.Marshal(map[string]any{"type": list.typ, "properties": lp})
			if err != nil {
				continue
			}
			for _, ev := range f.mapper.Map(v1map.Frame{Data: data}).Events {
				if pa, ok := ev.(convevents.PromptAsked); ok {
					seed.Prompts = append(seed.Prompts, pa.Prompt)
				}
			}
		}
	}
	f.sink.Connected(seed)
}

func (f *feeder) seedStatus(ctx context.Context) *convevents.SessionStatusEvent {
	if live := f.p.adapter.LiveStatus(f.p.key.SessionID); live != nil {
		switch db.SessionStatus(live.Status) {
		case db.StatusBusy, db.StatusWaiting:
			return &convevents.SessionStatusEvent{Status: db.StatusBusy}
		case db.StatusDone, db.StatusError, db.StatusInterrupted, db.StatusLimited:
			return &convevents.SessionStatusEvent{Status: db.SessionStatus(live.Status)}
		}
	}
	lc, err := f.p.Settle(ctx)
	if err != nil || lc == nil || lc.Status == "" {
		return nil
	}
	return &convevents.SessionStatusEvent{Status: lc.Status}
}

func (f *feeder) deliverLocked(res v1map.Result) {
	// Sequence admission happens before mapping, once per source frame.
	meta := Meta{Replayed: res.Replayed}
	for _, ev := range res.Events {
		f.sink.Emit(ev, meta)
	}
	if res.QueueChanged {
		f.sink.QueueChanged()
	}
	for _, ref := range res.Unanchored {
		f.refreshLocked(ref)
	}
}

// refreshLocked supplies a display snapshot, not a delta anchor. REST has no
// stream cursor and may already include queued deltas. Those remain suppressed
// until an upstream ordered part upsert establishes the mapper's base.
func (f *feeder) refreshLocked(ref v1map.PartRef) {
	if f.refreshed[ref.PartID] {
		return
	}
	if len(f.refreshed) >= 4096 {
		clear(f.refreshed)
	}
	f.refreshed[ref.PartID] = true
	ctx, cancel := context.WithTimeout(f.ctx, refreshTimeout)
	defer cancel()
	detail, err := f.p.adapter.Session(ctx, f.p.key.SessionID, 1, 0)
	if err != nil || detail == nil {
		return
	}
	for _, part := range detail.Parts {
		if part.ID != ref.PartID || part.MessageID != ref.MessageID {
			continue
		}
		var data map[string]json.RawMessage
		if json.Unmarshal(part.Data, &data) != nil {
			return
		}
		data["id"], _ = json.Marshal(part.ID)
		data["messageID"], _ = json.Marshal(part.MessageID)
		data["sessionID"], _ = json.Marshal(f.p.key.SessionID)
		raw, err := json.Marshal(map[string]any{"type": "message.part.updated", "properties": map[string]any{"part": data}})
		if err == nil {
			display := v1map.New(v1map.Options{SessionID: f.p.key.SessionID, Now: f.p.now})
			f.deliverLocked(display.Map(v1map.Frame{Data: raw}))
		}
		return
	}
}
