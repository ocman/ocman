package convstream

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
)

// model folds envelopes the way the client reducer does, for the fields
// compaction may affect.
type model struct {
	Parts      map[string]string
	Messages   map[string]bool
	Status     string
	Prompts    map[string]bool
	Judge      map[string]string
	Background int
	Queue      int
	Edges      []string
}

func newModel() *model {
	return &model{Parts: map[string]string{}, Messages: map[string]bool{}, Prompts: map[string]bool{}, Judge: map[string]string{}}
}

func (m *model) apply(env convevents.Envelope) {
	switch e := env.Event.(type) {
	case convevents.PartDelta:
		cur := m.Parts[e.PartID]
		switch {
		case e.Offset == len(cur):
			m.Parts[e.PartID] = cur + e.Text
		case e.Offset < len(cur) && len(cur)-e.Offset < len(e.Text):
			m.Parts[e.PartID] = cur + e.Text[len(cur)-e.Offset:]
		}
	case convevents.PartUpserted:
		text := readPart(e.Part.Data).Text
		if e.Final || len(text) >= len(m.Parts[e.Part.ID]) {
			m.Parts[e.Part.ID] = text
		}
	case convevents.MessageUpserted:
		m.Messages[e.Message.ID] = true
	case convevents.SessionStatusEvent:
		b, _ := json.Marshal(e)
		m.Status = string(b)
	case convevents.PromptAsked:
		m.Prompts[e.Prompt.ID] = true
	case convevents.PromptJudge:
		m.Judge[e.PromptID] = e.Phase
	case convevents.PromptResolved:
		delete(m.Prompts, e.PromptID)
		delete(m.Judge, e.PromptID)
	case convevents.BackgroundChanged:
		m.Background = e.Count
	case convevents.QueueChanged:
		m.Queue = len(e.Messages)
	case convevents.TurnStarted, convevents.TurnEnded:
		m.Edges = append(m.Edges, fmt.Sprintf("%s@%d", e.EventType(), env.Seq))
	}
}

// generate feeds a random but well-formed source stream: deltas carry the
// offsets the mapper would assign.
func generate(r *rand.Rand, sink Sink, n int) {
	acc := map[string]string{}
	open := map[string]bool{}
	for range n {
		part := fmt.Sprintf("p%d", r.IntN(4))
		pid := fmt.Sprintf("q%d", r.IntN(3))
		switch r.IntN(10) {
		case 0, 1, 2:
			text := string(rune('a' + r.IntN(26)))
			sink.Emit(delta(part, len(acc[part]), text), Meta{})
			acc[part] += text
		case 3:
			sink.Emit(textPart(part, acc[part], false), Meta{})
		case 4:
			acc[part] += "."
			sink.Emit(textPart(part, acc[part], true), Meta{})
		case 5:
			sink.Emit([]convevents.Event{status(db.StatusBusy), idle(), status(db.StatusError),
				convevents.SessionStatusEvent{Status: db.StatusLimited, ResetsAt: int64(r.IntN(9))}}[r.IntN(4)], Meta{})
		case 6:
			if !open[pid] {
				open[pid] = true
				sink.Emit(prompt(pid+fmt.Sprint(r.IntN(1000)), testKey.SessionID), Meta{})
			}
			sink.Emit(prompt(pid, testKey.SessionID), Meta{})
		case 7:
			sink.Emit(convevents.PromptJudge{PromptID: pid, Phase: convevents.JudgeChecking}, Meta{})
		case 8:
			sink.Emit(convevents.PromptResolved{PromptID: pid, Kind: convevents.PromptPermission, Outcome: "once", By: convevents.ByUser}, Meta{})
		case 9:
			sink.Emit(convevents.BackgroundChanged{Count: r.IntN(3)}, Meta{})
			sink.Emit(convevents.MessageUpserted{Message: db.Message{ID: fmt.Sprintf("m%d", r.IntN(3))}}, Meta{})
		}
	}
}

// TestCompactedReplayFoldsToUncompactedState checks design §13.3.4: for
// every resume point from the oldest retained seq − 1 on, the prefix the
// client saw plus the compacted replay equals the full uncompacted fold.
func TestCompactedReplayFoldsToUncompactedState(t *testing.T) {
	for seed := range uint64(6) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			h := newTestHub(t, Options{})
			src := newFakeSource()
			src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusDone}}
			sub := subscribe(t, h, bareSource{src}, "")
			sink := nextSink(t, src)
			h.mu.Lock()
			s := h.sessions[testKey]
			h.mu.Unlock()
			if seed%2 == 1 {
				s.mu.Lock()
				s.log.maxEntries = 60
				s.mu.Unlock()
			}

			var full []convevents.Envelope
			r := rand.New(rand.NewPCG(seed, 7))
			for range 30 {
				generate(r, sink, 10)
				full = append(full, decodeAll(t, drain(sub))...)
			}
			want := newModel()
			for _, env := range full {
				want.apply(env)
			}

			s.mu.Lock()
			floor, last, retained := s.log.floor, s.log.last, len(s.log.after(0))
			s.mu.Unlock()
			if retained >= len(full) {
				t.Fatalf("nothing compacted: %d retained of %d", retained, len(full))
			}
			if seed%2 == 1 && floor == 0 {
				t.Fatal("bounded log never evicted")
			}
			for k := floor; k <= last; k++ {
				resumed := subscribe(t, h, bareSource{src}, fmt.Sprintf("%s:%d", sub.Epoch, k))
				if !decode(t, resumed.Hello.frame).Event.(convevents.StreamHello).Resumed {
					t.Fatalf("resume at %d (floor %d) refused", k, floor)
				}
				got := newModel()
				for _, env := range full {
					if env.Seq <= k {
						got.apply(env)
					}
				}
				for _, env := range decodeAll(t, resumed.Replay) {
					got.apply(env)
				}
				resumed.Close()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("resume at %d: state\n%+v\nwant\n%+v", k, got, want)
				}
			}
			if floor > 0 {
				refused := subscribe(t, h, bareSource{src}, fmt.Sprintf("%s:%d", sub.Epoch, floor-1))
				if decode(t, refused.Hello.frame).Event.(convevents.StreamHello).Resumed {
					t.Fatal("resume before the floor accepted")
				}
			}
		})
	}
}
