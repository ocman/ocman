package convstream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestOneUpstreamFansOutToEverySubscriber(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	a := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	b := subscribe(t, h, src, "")
	sink.Emit(textPart("p1", "hi", false), Meta{})
	for _, sub := range []*Subscription{a, b} {
		got := decodeAll(t, drain(sub))
		if len(got) != 1 || got[0].Event.EventType() != convevents.TypePartUpserted || got[0].Seq != 1 {
			t.Fatalf("frames = %v", types(got))
		}
	}
	if w, _ := src.counts(); w != 1 {
		t.Fatalf("watches = %d, want one upstream", w)
	}
	hello := decode(t, b.Hello.frame)
	if hello.Event.(convevents.StreamHello).Resumed || hello.Seq != 0 || hello.SessionID != testKey.SessionID || hello.Platform != testKey.Platform {
		t.Fatalf("hello = %+v", hello)
	}
}

// eagerSource emits as soon as it connects, as a provider replaying the
// latest turn does, before a waiting subscriber has run again.
type eagerSource struct{ ev convevents.Event }

func (e eagerSource) Watch(ctx context.Context, sink Sink) error {
	sink.Connected(Seed{})
	sink.Emit(e.ev, Meta{})
	<-ctx.Done()
	return ctx.Err()
}

func TestSubscriberWaitingForConnectGetsEventsEmittedOnConnect(t *testing.T) {
	for range 20 {
		h := newTestHub(t, Options{})
		sub := subscribe(t, h, eagerSource{ev: textPart("p1", "SPIKE_OK", true)}, "")
		if hello := decode(t, sub.Hello.frame); hello.Seq != 0 {
			t.Fatalf("hello seq = %d: the first subscriber skipped what the source sent on connect", hello.Seq)
		}
		await(t, sub, convevents.TypePartUpserted)
	}
}

func TestLingerKeepsSourceThenDetaches(t *testing.T) {
	h := newTestHub(t, Options{Linger: 30 * time.Millisecond})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	nextSink(t, src)
	sub.Close()
	if _, active := src.counts(); active != 1 {
		t.Fatal("source detached before the linger")
	}
	again := subscribe(t, h, src, "")
	again.Close()
	deadline := time.Now().Add(waitFor)
	for {
		if _, active := src.counts(); active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source never detached after the linger")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w, _ := src.counts(); w != 1 {
		t.Fatalf("watches = %d; a resubscribe within the linger must reuse the upstream", w)
	}
}

func TestResumeWithinAndBeyondRetention(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	for i := range 5 {
		sink.Emit(textPart("p"+string(rune('a'+i)), "x", true), Meta{})
	}
	frames := drain(sub)
	cursor := decode(t, frames[2].frame).Cursor()

	resumed := subscribe(t, h, src, cursor)
	hello := decode(t, resumed.Hello.frame)
	if !hello.Event.(convevents.StreamHello).Resumed || hello.Cursor() != cursor {
		t.Fatalf("hello = %+v, want resumed at %s", hello, cursor)
	}
	if got := decodeAll(t, resumed.Replay); len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("replay = %+v", got)
	}

	for _, after := range []string{"other:3", sub.Epoch + ":99", "garbage"} {
		cold := subscribe(t, h, src, after)
		ev := decode(t, cold.Hello.frame).Event.(convevents.StreamHello)
		if ev.Resumed || ev.Overlay == nil || len(cold.Replay) != 0 {
			t.Fatalf("after %q: hello = %+v", after, ev)
		}
	}

	h.mu.Lock()
	s := h.sessions[testKey]
	h.mu.Unlock()
	s.mu.Lock()
	s.log.maxEntries = 10
	s.mu.Unlock()
	for i := range 20 {
		sink.Emit(delta("p_long", i, "y"), Meta{})
	}
	evicted := subscribe(t, h, src, cursor)
	if decode(t, evicted.Hello.frame).Event.(convevents.StreamHello).Resumed {
		t.Fatal("a cursor older than the retained log must not resume")
	}
}

func TestEpochChangesWhenSourceRestarts(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(textPart("p1", "a", true), Meta{})
	old := decode(t, drain(sub)[0].frame).Cursor()

	src.end <- errBoom
	errFrame := await(t, sub, convevents.TypeStreamError)
	if e := errFrame[len(errFrame)-1].Event.(convevents.StreamError); e.Code != convevents.CodePlatformUnreachable || !e.Retryable {
		t.Fatalf("stream.error = %+v", e)
	}
	sink2 := nextSink(t, src)
	select {
	case <-sub.Done():
	case <-time.After(waitFor):
		t.Fatal("subscriber not reset on reconnect")
	}
	if sub.Reason() != convevents.ResetSourceRestarted {
		t.Fatalf("reason = %q", sub.Reason())
	}
	sink.Emit(textPart("stale", "x", true), Meta{}) // the old connection's sink is stale
	fresh := subscribe(t, h, src, old)
	if fresh.Epoch == sub.Epoch || decode(t, fresh.Hello.frame).Event.(convevents.StreamHello).Resumed {
		t.Fatal("a reconnected source must start a new epoch")
	}
	sink2.Emit(textPart("p2", "b", true), Meta{})
	if got := decodeAll(t, drain(fresh)); len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("new epoch frames = %v", types(got))
	}
}

func TestSlowConsumerIsReset(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	slow := subscribe(t, h, src, "")
	fast := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	for i := range subscriberBuffer + 10 {
		sink.Emit(delta("p", i, "z"), Meta{})
		drain(fast)
	}
	select {
	case <-slow.Done():
	default:
		t.Fatal("slow subscriber not reset")
	}
	if slow.Reason() != convevents.ResetSlowConsumer {
		t.Fatalf("reason = %q", slow.Reason())
	}
	select {
	case <-fast.Done():
		t.Fatal("a keeping-up subscriber was reset")
	default:
	}
	frame := decode(t, slow.controlFrame(convevents.StreamReset{Reason: slow.Reason()}, 7))
	if frame.Seq != 7 || frame.Event.(convevents.StreamReset).Reason != convevents.ResetSlowConsumer {
		t.Fatalf("reset frame = %+v", frame)
	}
}

func TestSourceFailingBeforeConnectFailsSubscribe(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.failErr = platforms.ErrPlatformUnreachable
	_, err := h.Subscribe(t.Context(), testKey, func() Source { return src }, "")
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("err = %v", err)
	}
	h.mu.Lock()
	n := len(h.sessions)
	h.mu.Unlock()
	if n != 0 {
		t.Fatal("a failed session must not linger")
	}
	src.failErr = nil
	subscribe(t, h, src, "")
	nextSink(t, src)
}

func TestSubscribeHonoursContext(t *testing.T) {
	h := newTestHub(t, Options{})
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	src := sourceFunc(func(ctx context.Context, _ Sink) error {
		select {
		case <-ctx.Done():
		case <-block:
		}
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Subscribe(ctx, testKey, func() Source { return src }, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	h.Close()
	if _, err := h.Subscribe(t.Context(), testKey, func() Source { return src }, ""); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close err = %v", err)
	}
}

type sourceFunc func(context.Context, Sink) error

func (f sourceFunc) Watch(ctx context.Context, s Sink) error { return f(ctx, s) }

func TestPublishAndQueueRead(t *testing.T) {
	read := make(chan Key, 1)
	h := newTestHub(t, Options{Queue: func(_ context.Context, key Key) ([]convevents.QueuedMessage, error) {
		read <- key
		return []convevents.QueuedMessage{{ID: "q1", Text: "later"}}, nil
	}})
	h.Publish(testKey, convevents.ChangesDirty{Reason: convevents.DirtyCommit}) // not attached: dropped
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	h.Publish(testKey, convevents.ChangesDirty{Reason: convevents.DirtyCommit})
	sink.QueueChanged()
	if k := <-read; k != testKey {
		t.Fatalf("queue read for %v", k)
	}
	got := await(t, sub, convevents.TypeQueueChanged)
	if types(got)[0] != convevents.TypeChangesDirty || len(got) != 2 {
		t.Fatalf("frames = %v", types(got))
	}
	hello := decode(t, subscribe(t, h, src, "").Hello.frame).Event.(convevents.StreamHello)
	if hello.Overlay.Queue == nil || hello.Overlay.Queue.Messages[0].ID != "q1" {
		t.Fatalf("overlay queue = %+v", hello.Overlay.Queue)
	}
}

func TestSourceReplayProvenance(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(textPart("p1", "a", false), Meta{})
	sink.Emit(textPart("p1", "ab", false), Meta{Replayed: true})
	got := decodeAll(t, drain(sub))
	if len(got) != 2 || got[0].Replayed || !got[1].Replayed {
		t.Fatalf("frames = %+v", got)
	}
}

func TestHelloOverlayRestatesLiveState(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(convevents.BackgroundChanged{Count: 1, Tasks: []convevents.BackgroundTask{{ID: "t", Description: "shell"}}}, Meta{})
	sink.Emit(convevents.MessageUpserted{Message: db.Message{ID: "msg_1", SessionID: testKey.SessionID}}, Meta{})
	sink.Emit(textPart("p1", "he", false), Meta{})
	sink.Emit(delta("p1", 2, "llo"), Meta{})
	sink.Emit(delta("p1", 3, "lo!"), Meta{}) // overlapping replay
	sink.Emit(prompt("perm1", testKey.SessionID), Meta{})
	sink.Emit(convevents.PromptJudge{PromptID: "perm1", Phase: convevents.JudgeChecking}, Meta{})

	ov := decode(t, subscribe(t, h, src, "").Hello.frame).Event.(convevents.StreamHello).Overlay
	if ov.Status.Status != db.StatusWaiting || ov.Background.Count != 1 || !ov.LiveConnection {
		t.Fatalf("overlay = %+v", ov)
	}
	if len(ov.Prompts) != 1 || len(ov.Judge) != 1 || ov.Judge[0].Phase != convevents.JudgeChecking {
		t.Fatalf("prompts = %+v judge = %+v", ov.Prompts, ov.Judge)
	}
	if len(ov.Inflight.Messages) != 1 || len(ov.Inflight.Parts) != 1 || readPart(ov.Inflight.Parts[0].Data).Text != "hello!" {
		t.Fatalf("inflight = %+v", ov.Inflight)
	}
}
