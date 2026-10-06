package factory

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/sirupsen/logrus"
)

const (
	factoryDispatchRecoveryInterval = 5 * time.Minute
	factoryDispatchRetryInterval    = 15 * time.Second
)

func (s *NativeService) wakeDispatch() {
	select {
	case s.dispatchWake <- struct{}{}:
	default:
	}
}

// NotifySessionIdle wakes only Factory implementation sessions. Ordinary session
// activity must not turn the recovery scan into a global event-driven poll.
func (s *NativeService) NotifySessionIdle(platform, sessionID string) {
	if _, tracked := s.activeSessions.Load(PlanningSession{Platform: platform, ID: sessionID}); tracked {
		s.wakeDispatch()
	}
}

func (s *NativeService) rememberImplementationSessions(attempts []model.FactoryAttempt) {
	active := make(map[PlanningSession]bool)
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptActive && attempt.FrozenPolicy.Profile == "factory-implement/v1" && attempt.Session.ID != "" {
			active[attempt.Session] = true
			s.activeSessions.Store(attempt.Session, struct{}{})
		}
	}
	s.activeSessions.Range(func(key, _ any) bool {
		if !active[key.(PlanningSession)] {
			s.activeSessions.Delete(key)
		}
		return true
	})
}

// Persisted retry deadlines and outstanding merge gates get their own timer.
// Everything else waits for an event or the five-minute recovery scan.
func (s *NativeService) nextDispatchDelay(ctx context.Context, recovery time.Duration) time.Duration {
	store, ok := s.store.(interface {
		NextFactoryDispatchAt(context.Context, time.Duration) (time.Time, error)
	})
	if !ok {
		return recovery
	}
	next, err := store.NextFactoryDispatchAt(ctx, factoryMergeGatePollInterval)
	if err != nil {
		logrus.WithError(err).Error("Factory dispatch deadline failed")
		return min(recovery, factoryDispatchRetryInterval)
	}
	if next.IsZero() {
		return recovery
	}
	return min(recovery, max(time.Until(next), time.Second))
}
