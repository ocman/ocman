package convstream

import (
	"context"
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestIdleEdgeSettlesThroughLifecycle(t *testing.T) {
	cases := []struct {
		name string
		lc   *platforms.SessionLifecycle
		err  error
		want []string
	}{
		{"error", &platforms.SessionLifecycle{Status: db.StatusError, LatestMessageID: "msg_9"}, nil,
			[]string{"session.status:done?", "turn.ended:error", "session.status:error"}},
		{"interrupted", &platforms.SessionLifecycle{Status: db.StatusInterrupted}, nil,
			[]string{"session.status:done?", "turn.ended:interrupted", "session.status:interrupted"}},
		{"done", &platforms.SessionLifecycle{Status: db.StatusDone}, nil,
			[]string{"session.status:done?", "turn.ended:done", "session.status:done"}},
		{"read fails keeps provisional", nil, errBoom,
			[]string{"session.status:done?", "turn.ended:done"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHub(t, Options{})
			src := newFakeSource()
			src.lc, src.lcErr = tc.lc, tc.err
			src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusDone}}
			sub := subscribe(t, h, src, "")
			sink := nextSink(t, src)
			sink.Emit(status(db.StatusBusy), Meta{})
			if got := types(decodeAll(t, drain(sub))); !slices.Equal(got, []string{"turn.started", "session.status:busy"}) {
				t.Fatalf("busy edge = %v", got)
			}
			sink.Emit(idle(), Meta{})
			got := await(t, sub, convevents.TypeTurnEnded)
			if tc.lc != nil {
				got = append(got, await(t, sub, convevents.TypeSessionStatus)...)
			}
			got = append(got, decodeAll(t, drain(sub))...)
			if !slices.Equal(types(got), tc.want) {
				t.Fatalf("idle edge = %v, want %v", types(got), tc.want)
			}
			if tc.lc != nil {
				if te := got[1].Event.(convevents.TurnEnded); te.LatestMessageID != tc.lc.LatestMessageID {
					t.Fatalf("turn.ended = %+v", te)
				}
			}
		})
	}
}

func TestNewTurnBeforeSettleEndsPreviousTurn(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.lc = &platforms.SessionLifecycle{Status: db.StatusError}
	src.block = make(chan struct{})
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(idle(), Meta{})
	sink.Emit(status(db.StatusBusy), Meta{})
	close(src.block)
	<-src.settled
	got := types(decodeAll(t, drain(sub)))
	want := []string{"session.status:done?", "turn.ended:done", "turn.started", "session.status:busy"}
	if !slices.Equal(got, want) {
		t.Fatalf("frames = %v, want %v; an overtaken settle must be dropped", got, want)
	}
}

func TestSourceWithoutLifecycleEndsTurnUnsettled(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	// A source without a lifecycle read settles synchronously as unsettled.
	sub := subscribe(t, h, bareSource{src}, "")
	sink := nextSink(t, src)
	sink.Emit(idle(), Meta{Replayed: true})
	sink.Emit(convevents.BackgroundChanged{Count: 0}, Meta{})
	sink.Emit(status(db.StatusBusy), Meta{})
	got := decodeAll(t, drain(sub))
	want := []string{"session.status:done?", "turn.ended:done", "turn.started", "session.status:busy"}
	if !slices.Equal(types(got), want) {
		t.Fatalf("frames = %v, want %v", types(got), want)
	}
	if !got[1].Replayed {
		t.Fatal("turn.ended of a replayed idle edge must be marked replayed")
	}
}

func TestBusyInferredFromRunningContent(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusDone}}
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(toolPart("t1", "completed"), Meta{})
	sink.Emit(toolPart("t2", "running"), Meta{})
	sink.Emit(delta("t2", 0, "out"), Meta{})
	want := []string{"part.upserted", "turn.started", "session.status:busy", "part.upserted", "part.delta"}
	if got := types(decodeAll(t, drain(sub))); !slices.Equal(got, want) {
		t.Fatalf("frames = %v, want %v", got, want)
	}
}

func TestTerminalStatusFromSourceEndsTurnAndClearsBackground(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(convevents.BackgroundChanged{Count: 2}, Meta{})
	sink.Emit(convevents.BackgroundChanged{Count: 2}, Meta{})
	sink.Emit(status(db.StatusError), Meta{})
	sink.Emit(convevents.SessionStatusEvent{Status: db.StatusLimited, ResetsAt: 99}, Meta{})
	want := []string{"background.changed", "turn.ended:error", "session.status:error", "session.status:limited"}
	got := decodeAll(t, drain(sub))
	if !slices.Equal(types(got), want) {
		t.Fatalf("frames = %v, want %v", types(got), want)
	}
	if st := got[3].Event.(convevents.SessionStatusEvent); st.ResetsAt != 99 || !st.Notice.Set {
		t.Fatalf("limited = %+v", st)
	}
	sink.Emit(idle(), Meta{})
	if got := types(decodeAll(t, drain(sub))); !slices.Equal(got, []string{"background.changed", "session.status:done?"}) {
		t.Fatalf("idle = %v", got)
	}
}

func TestWaitingDerivedFromRoutedPrompts(t *testing.T) {
	asked := map[string]bool{}
	h := newTestHub(t, Options{Descendant: func(_ context.Context, root Key, child string) bool {
		asked[child] = true
		return root == testKey && child == "ses_child"
	}})
	src := newFakeSource()
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}, Prompts: []convevents.Prompt{prompt("seeded", "ses_stranger").Prompt}}
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	if ov := decode(t, sub.Hello.frame).Event.(convevents.StreamHello).Overlay; len(ov.Prompts) != 0 || ov.Status.Status != db.StatusBusy {
		t.Fatalf("an unrelated session's seeded prompt was routed: %+v", ov)
	}
	sink.Emit(prompt("p_other", "ses_stranger"), Meta{})
	sink.Emit(prompt("p_child", "ses_child"), Meta{})
	sink.Emit(prompt("p_child", "ses_child"), Meta{})
	sink.Emit(convevents.PromptJudge{PromptID: "p_child", Phase: convevents.JudgeScheduled, StartsAt: 5}, Meta{})
	sink.Emit(convevents.PromptResolved{PromptID: "p_child", Kind: convevents.PromptPermission, Outcome: "once", By: convevents.ByAI}, Meta{})
	sink.Emit(convevents.PromptResolved{PromptID: "p_child", Kind: convevents.PromptPermission, Outcome: "once", By: convevents.ByUser}, Meta{})
	sink.Emit(convevents.PromptJudge{PromptID: "p_child", Phase: convevents.JudgeFlagged}, Meta{})
	want := []string{"prompt.asked", "session.status:waiting", "prompt.asked", "prompt.judge", "prompt.resolved", "session.status:busy"}
	got := decodeAll(t, drain(sub))
	if !slices.Equal(types(got), want) {
		t.Fatalf("frames = %v, want %v", types(got), want)
	}
	if got[4].Event.(convevents.PromptResolved).By != convevents.ByAI {
		t.Fatal("the first resolution wins")
	}
	if !asked["ses_stranger"] || !asked["ses_child"] {
		t.Fatalf("resolver calls = %v", asked)
	}
}

func TestSettleUnknownSeedOnConnect(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	src.lc = &platforms.SessionLifecycle{Status: db.StatusError}
	src.block = make(chan struct{})
	sub := subscribe(t, h, src, "")
	nextSink(t, src)
	if st := decode(t, sub.Hello.frame).Event.(convevents.StreamHello).Overlay.Status; !st.Provisional {
		t.Fatalf("unknown status = %+v, want provisional", st)
	}
	close(src.block)
	got := await(t, sub, convevents.TypeSessionStatus)
	if st := got[0].Event.(convevents.SessionStatusEvent); st.Status != db.StatusError || st.Provisional {
		t.Fatalf("settled status = %+v", st)
	}
}
