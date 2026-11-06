package convstream

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/telemetry"
)

type settlementRead struct {
	settler    Settler
	gen, epoch uint64
	replayed   bool
}

type settlementState struct {
	reading bool
	pending *settlementRead
}

func (s *session) scheduleSettleLocked(gen uint64, replayed bool) {
	settler, ok := s.source.(Settler)
	if !ok {
		s.settledLocked(gen, nil, platforms.ErrUnsupported, replayed)
		return
	}
	if s.detached {
		return
	}
	request := &settlementRead{settler: settler, gen: gen, epoch: s.gen, replayed: replayed}
	s.settlement.pending = request
	if s.settlement.reading {
		return
	}
	s.settlement.reading = true
	if !s.hub.opts.Workers.Go(func(ctx context.Context) { s.readSettlement(ctx, request) }) {
		s.settlement = settlementState{}
		s.settledLocked(gen, nil, ErrClosed, replayed)
	}
}

func (s *session) readSettlement(ctx context.Context, request *settlementRead) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.mu.Lock()
			s.settlement = settlementState{}
			s.mu.Unlock()
			panic(recovered) // the owning Workers recovery logs the failure
		}
	}()
	for request != nil {
		readCtx, cancel := context.WithTimeout(ctx, settleTimeout)
		finish := telemetry.ConversationWork(readCtx, telemetry.ConversationSettle)
		lc, err := request.settler.Settle(readCtx)
		finish(err, 1)
		cancel()
		request = s.finishSettlement(ctx, request, lc, err)
	}
}

func (s *session) finishSettlement(ctx context.Context, request *settlementRead, lc *platforms.SessionLifecycle, err error) *settlementRead {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.settlement.pending
	live := !s.detached && ctx.Err() == nil
	if live && next != request && next.gen == s.st.gen && next.epoch == s.gen {
		return next
	}
	s.settlement = settlementState{}
	if live && request.epoch == s.gen {
		s.settledLocked(request.gen, lc, err, request.replayed)
	}
	return nil
}
