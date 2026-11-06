package queuesvc

import "context"

// WithStartGate holds follow-ups while durable setup or first delivery is pending.
// Configure once before serving, alongside WithAdmission.
func (s *Service) WithStartGate(blocked func(context.Context, string) bool) {
	s.startBlocked = blocked
}

// TryFlush reconciles setup completion without pretending the first turn is idle.
func (s *Service) TryFlush(ctx context.Context, platform, id string) {
	key := sessionKey{Platform: platform, SessionID: id}
	unlock := s.lockFor(key)
	defer unlock()
	if _, guarded := s.currentDrainGuard(key); guarded {
		return
	}
	s.drainHead(ctx, key, false, nil)
}
