package sessionsvc

import (
	"context"
	"sync"
)

type routinePublication struct {
	mu      sync.Mutex
	pending map[chan struct{}]struct{}
}

// BeginRoutineCreation fences owner-created worktree sessions until run linkage
// is durable, just like CreateRoutine's configured-session path.
func (s *Service) BeginRoutineCreation() func() { return s.routinePublication.begin() }

func (p *routinePublication) begin() func() {
	done := make(chan struct{})
	p.mu.Lock()
	if p.pending == nil {
		p.pending = make(map[chan struct{}]struct{})
	}
	p.pending[done] = struct{}{}
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.pending, done)
		close(done)
		p.mu.Unlock()
	}
}

// WaitForRoutineCreation protects lists already read from upstream: OpenCode
// announces a row before returning its ID, so it cannot be classified yet.
// Call after reading upstream rows and before reading routine associations.
func (s *Service) WaitForRoutineCreation(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.routinePublication.mu.Lock()
	pending := make([]chan struct{}, 0, len(s.routinePublication.pending))
	for done := range s.routinePublication.pending {
		pending = append(pending, done)
	}
	s.routinePublication.mu.Unlock()
	// ponytail: lists wait for all in-flight routine creates; scope by owner
	// and directory if slow launches make unrelated lists wait noticeably.
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
