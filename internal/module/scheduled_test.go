package module

import (
	"context"
	"errors"
	"testing"
	"time"
)

type observingSchedule struct{ seen []Schedule }

type stoppingSchedule struct {
	observingSchedule
	stopped string
	err     error
}

func (o *stoppingSchedule) StopSchedule(_ context.Context, name string) error {
	o.stopped = name
	return o.err
}

func (o *observingSchedule) ObserveSchedule(ctx context.Context, s Schedule, work func(context.Context) error) error {
	o.seen = append(o.seen, s)
	return work(ctx)
}

func TestEveryObservesEachRecurringIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observer := &observingSchedule{}
	ctx = WithScheduleObserver(ctx, observer)
	Every(ctx, "worktree-cleanup", time.Millisecond, func(context.Context) {
		if len(observer.seen) == 2 {
			cancel()
		}
	})
	if len(observer.seen) != 2 || observer.seen[0].Title != "Clean archived worktrees" || observer.seen[0].Interval != time.Millisecond {
		t.Fatalf("observed schedules: %+v", observer.seen)
	}
}

func TestScheduledDirectWorkAndCancellation(t *testing.T) {
	want := errors.New("work failed")
	if err := RunScheduled(t.Context(), Schedule{}, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RunScheduled(ctx, Schedule{}, func(context.Context) error { t.Fatal("cancelled job ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if title := ScheduleTitle("new-job"); title != "Check new job" {
		t.Fatal(title)
	}
	if err := ReportProgress(t.Context(), Progress{}); err != nil {
		t.Fatal(err)
	}
	ReportError(t.Context(), nil)
	ReportError(t.Context(), want)
}

func TestEveryFinalizesScheduleEvenAfterPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observer := &stoppingSchedule{err: errors.New("cannot save stopped hint")}
	ctx = WithScheduleObserver(ctx, observer)
	Every(ctx, "panic-check", time.Hour, func(context.Context) { cancel(); panic("boom") })
	if observer.stopped != "panic-check" {
		t.Fatalf("loop did not finalize: %+v", observer)
	}
	StopSchedule(t.Context(), "unobserved")
	StopSchedule(WithScheduleObserver(t.Context(), observer), "error-check")
	if observer.stopped != "error-check" {
		t.Fatal(observer.stopped)
	}
}
