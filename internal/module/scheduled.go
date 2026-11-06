package module

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

// Schedule describes an owner-local recurring job, not its timer or executor.
type Schedule struct {
	Name, Title, Scope string
	Interval           time.Duration
}

// StopSchedule clears a recurring job's next-run hint when its loop exits.
func StopSchedule(ctx context.Context, name string) {
	if observer, ok := ctx.Value(scheduleObserverKey{}).(interface {
		StopSchedule(context.Context, string) error
	}); ok {
		if err := observer.StopSchedule(ctx, name); err != nil && ctx.Err() == nil {
			log.WithError(err).WithField("job", name).Warn("stopping scheduled task observation")
		}
	}
}

type ScheduleObserver interface {
	ObserveSchedule(context.Context, Schedule, func(context.Context) error) error
}

type scheduleObserverKey struct{}

func WithScheduleObserver(ctx context.Context, observer ScheduleObserver) context.Context {
	return context.WithValue(ctx, scheduleObserverKey{}, observer)
}

// RunScheduled retains the caller's execution and cancellation semantics.
// Observation must not enqueue jobs behind the work they inspect or maintain.
func RunScheduled(ctx context.Context, schedule Schedule, work func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if observer, ok := ctx.Value(scheduleObserverKey{}).(ScheduleObserver); ok {
		return observer.ObserveSchedule(ctx, schedule, work)
	}
	return work(ctx)
}

// Scheduled observes one iteration of an existing timer or event-driven loop.
func Scheduled(ctx context.Context, name string, interval time.Duration, work func(context.Context)) {
	RunWithRecover(name, func() {
		err := RunScheduled(ctx, Schedule{Name: name, Title: ScheduleTitle(name), Interval: interval}, func(ctx context.Context) error { work(ctx); return nil })
		if err != nil && ctx.Err() == nil {
			log.WithError(err).WithField("job", name).Warn("scheduled background task failed")
		}
	})
}
