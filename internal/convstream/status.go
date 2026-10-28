package convstream

import (
	"context"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// statusState settles the source's raw lifecycle into status levels and
// turn edges (design §4.4).
type statusState struct {
	// base is the status before waiting is derived from open prompts.
	base    convevents.SessionStatusEvent
	emitted convevents.SessionStatusEvent
	// turn is set between turn.started and the idle edge; pendingEnd from
	// the idle edge until turn.ended.
	turn, pendingEnd bool
	// gen invalidates settle reads overtaken by a newer lifecycle edge.
	gen uint64
}

func provisionalDone() convevents.SessionStatusEvent {
	return convevents.SessionStatusEvent{Status: db.StatusDone, Provisional: true, Notice: convevents.Null[db.SessionNotice]()}
}

func newStatusState() statusState {
	return statusState{base: provisionalDone(), emitted: provisionalDone()}
}

// absolute makes a status event replace every status field, so a later
// one fully supersedes it.
func absolute(ev convevents.SessionStatusEvent) convevents.SessionStatusEvent {
	if !ev.Notice.Set {
		ev.Notice = convevents.Null[db.SessionNotice]()
	}
	if ev.Status != db.StatusLimited {
		ev.ResetsAt = 0
	}
	return ev
}

func (st *statusState) seed(ev convevents.SessionStatusEvent) {
	st.base = absolute(ev)
	st.turn = ev.Status == db.StatusBusy
}

func sameStatus(a, b convevents.SessionStatusEvent) bool {
	if a.Status != b.Status || a.Provisional != b.Provisional || a.ResetsAt != b.ResetsAt || a.Notice.Set != b.Notice.Set {
		return false
	}
	if a.Notice.Value == nil || b.Notice.Value == nil {
		return a.Notice.Value == b.Notice.Value
	}
	return *a.Notice.Value == *b.Notice.Value
}

// effective derives waiting: any open prompt, the session's or a
// descendant's, holds the session waiting.
func (s *session) effective() convevents.SessionStatusEvent {
	if len(s.ov.prompts) > 0 {
		return convevents.SessionStatusEvent{Status: db.StatusWaiting, Notice: convevents.Null[db.SessionNotice]()}
	}
	return s.st.base
}

func (s *session) emitStatusLocked(replayed bool) {
	e := s.effective()
	if sameStatus(e, s.st.emitted) {
		return
	}
	s.st.emitted = e
	s.publishLocked(e, replayed)
}

func (s *session) onStatusLocked(ev convevents.SessionStatusEvent, replayed bool) {
	switch {
	case ev.Provisional && ev.Status == db.StatusDone:
		s.idleLocked(replayed)
		return
	case ev.Status == db.StatusBusy:
		s.startTurnLocked(replayed)
	case ev.Status == db.StatusDone || ev.Status == db.StatusError || ev.Status == db.StatusInterrupted:
		s.endTurnLocked(string(ev.Status), "", replayed)
		s.ledger.settle()
	}
	s.st.base = absolute(ev)
	s.emitStatusLocked(replayed)
}

// idleLocked publishes a provisional done at once, preserves background and
// settles the terminal kind off the lock.
func (s *session) idleLocked(replayed bool) {
	s.st.gen++
	if s.st.turn {
		s.st.turn, s.st.pendingEnd = false, true
	}
	s.st.base = provisionalDone()
	s.ledger.settle()
	s.emitStatusLocked(replayed)
	s.scheduleSettleLocked(s.st.gen, replayed)
}

func (s *session) scheduleSettleLocked(gen uint64, replayed bool) {
	settler, ok := s.source.(Settler)
	if !ok {
		s.settledLocked(gen, nil, platforms.ErrUnsupported, replayed)
		return
	}
	started := s.hub.opts.Workers.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, settleTimeout)
		lc, err := settler.Settle(ctx)
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.settledLocked(gen, lc, err, replayed)
	})
	if !started {
		s.settledLocked(gen, nil, ErrClosed, replayed)
	}
}

// settledLocked ends the pending turn with the settled outcome. A failed
// read ends it as done and keeps the provisional level (design Q5).
func (s *session) settledLocked(gen uint64, lc *platforms.SessionLifecycle, err error, replayed bool) {
	if s.detached || gen != s.st.gen {
		return
	}
	outcome, status, ok := settledOutcome(lc, err)
	if s.st.pendingEnd {
		s.st.pendingEnd = false
		latest := ""
		if lc != nil {
			latest = lc.LatestMessageID
		}
		s.publishLocked(convevents.TurnEnded{Outcome: outcome, EndedAt: s.now(), LatestMessageID: latest}, replayed)
	}
	if ok && status != db.StatusWaiting {
		s.ledger.settle()
	}
	if ok && s.st.base.Provisional {
		s.st.base = convevents.SessionStatusEvent{Status: status, Notice: convevents.Null[db.SessionNotice]()}
		s.emitStatusLocked(replayed)
	}
}

func settledOutcome(lc *platforms.SessionLifecycle, err error) (outcome string, status db.SessionStatus, ok bool) {
	if err != nil || lc == nil {
		return convevents.OutcomeDone, "", false
	}
	switch lc.Status {
	case db.StatusError:
		return convevents.OutcomeError, db.StatusError, true
	case db.StatusInterrupted:
		return convevents.OutcomeInterrupted, db.StatusInterrupted, true
	case db.StatusWaiting:
		return convevents.OutcomeDone, db.StatusWaiting, true
	case db.StatusDone:
		return convevents.OutcomeDone, db.StatusDone, true
	}
	// busy (a lagging read) or limited: the level stays provisional.
	return convevents.OutcomeDone, "", false
}

// startTurnLocked opens a turn, first ending one whose settle is pending.
func (s *session) startTurnLocked(replayed bool) {
	if s.st.pendingEnd {
		s.st.pendingEnd = false
		s.st.gen++
		s.publishLocked(convevents.TurnEnded{Outcome: convevents.OutcomeDone, EndedAt: s.now()}, replayed)
	}
	if !s.st.turn {
		s.st.turn = true
		s.publishLocked(convevents.TurnStarted{StartedAt: s.now()}, replayed)
	}
}

func (s *session) endTurnLocked(outcome, latest string, replayed bool) {
	if !s.st.turn && !s.st.pendingEnd {
		return
	}
	s.st.gen++
	s.st.turn, s.st.pendingEnd = false, false
	s.publishLocked(convevents.TurnEnded{Outcome: outcome, EndedAt: s.now(), LatestMessageID: latest}, replayed)
}

// handleLocked folds ev into the live state and publishes what follows.
func (s *session) handleLocked(ev convevents.Event, replayed bool) {
	if s.detached {
		return
	}
	switch e := ev.(type) {
	case convevents.SessionStatusEvent:
		s.onStatusLocked(e, replayed)
		return
	case convevents.StreamHello, convevents.StreamReset, convevents.StreamError, convevents.Unknown:
		return
	case convevents.PartDelta, convevents.PartUpserted, convevents.MessageUpserted,
		convevents.MessageRemoved, convevents.PartRemoved, convevents.HistoryRewritten:
		s.ledger.apply(ev, s.hub.opts.Now())
	case convevents.PromptAsked:
		if !s.ov.ask(e.Prompt) {
			return
		}
		s.publishLocked(e, replayed)
		s.emitStatusLocked(replayed)
		return
	case convevents.PromptResolved:
		if !s.ov.resolve(e.PromptID) {
			return
		}
		s.publishLocked(e, replayed)
		s.emitStatusLocked(replayed)
		return
	case convevents.PromptJudge:
		if s.ov.resolved[e.PromptID] {
			return
		}
		s.ov.judge[e.PromptID] = e
	case convevents.BackgroundChanged:
		if e.Tasks == nil {
			e.Tasks = []convevents.BackgroundTask{}
		}
		if sameBackground(e, s.ov.background) {
			return
		}
		s.ov.background = e
		ev = e
	case convevents.QueueChanged:
		s.ov.queue = &e
	}
	s.publishLocked(ev, replayed)
}

func sameBackground(a, b convevents.BackgroundChanged) bool {
	if a.Count != b.Count || len(a.Tasks) != len(b.Tasks) {
		return false
	}
	for i := range a.Tasks {
		if a.Tasks[i] != b.Tasks[i] {
			return false
		}
	}
	return true
}
