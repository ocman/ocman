package factory

import (
	"context"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// ponytail: one global idle limit. Make it a Formula/Epic setting when a
// workload legitimately runs a single silent command longer than this.
var (
	factoryIdleTimeout       = 30 * time.Minute
	factoryIdleProbeInterval = time.Minute
)

// ImplementationActivityProbe is implemented by launchers that can tell when
// a session last made progress and whether it is waiting on a human.
type ImplementationActivityProbe interface {
	ImplementationActivity(context.Context, PlanningSession) (lastActivity time.Time, waitingOnUser bool, err error)
}

type attemptResumeStore interface {
	FactoryAttemptLastResumedAt(context.Context, string) (time.Time, error)
}

// pauseIdleAttempt opens a recovery gate for a live attempt that stopped
// making progress without completing or asking for help. A dead session is
// handled by the runtime probe; this covers the hung-but-alive one.
func (s *NativeService) pauseIdleAttempt(ctx context.Context, attempt model.FactoryAttempt) error {
	probe, ok := s.implementation.(ImplementationActivityProbe)
	store, storeOK := s.store.(nativeRecoveryStore)
	if !ok || !storeOK || time.Since(time.UnixMilli(attempt.StartedAt)) < factoryIdleTimeout {
		return nil
	}
	s.checksMu.Lock()
	if s.idleProbedAt == nil {
		s.idleProbedAt = map[string]time.Time{}
	}
	run, checking := s.checks[attempt.ID]
	recent := time.Since(s.idleProbedAt[attempt.ID]) < factoryIdleProbeInterval
	if !recent {
		s.idleProbedAt[attempt.ID] = time.Now()
	}
	s.checksMu.Unlock()
	// Formula checks run outside the session and may be long and silent.
	if recent || (checking && !run.done) {
		return nil
	}
	since := time.UnixMilli(attempt.StartedAt)
	if resumes, ok := s.store.(attemptResumeStore); ok {
		resumed, err := resumes.FactoryAttemptLastResumedAt(ctx, attempt.ID)
		if err != nil {
			return err
		}
		if resumed.After(since) {
			since = resumed
		}
	}
	if time.Since(since) < factoryIdleTimeout {
		return nil
	}
	last, waiting, err := probe.ImplementationActivity(ctx, attempt.Session)
	if err != nil || waiting {
		return nil
	}
	if last.Before(since) {
		last = since
	}
	idle := time.Since(last)
	if idle < factoryIdleTimeout {
		return nil
	}
	question := fmt.Sprintf("The session has made no progress for %s without completing or requesting recovery. Resume it with guidance, retry the Issue, or cancel it.", idle.Round(time.Minute))
	_, err = store.CreateFactoryRecoveryGate(ctx, attempt.ID, question, "Factory watchdog: session idle", nil, time.Now())
	return err
}
