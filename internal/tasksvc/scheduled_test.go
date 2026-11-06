package tasksvc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module"
)

var exampleSchedule = module.Schedule{Name: "maintenance", Title: "Check maintenance", Interval: time.Hour}

func TestScheduledTasksCoalesceAndRetainProgress(t *testing.T) {
	m, _ := testManager(t, 1)
	var id string
	for run := int64(1); run <= 2; run++ {
		err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
			total := int64(3)
			return module.ReportProgress(ctx, module.Progress{Phase: "checking", Completed: 3, Total: &total, Unit: "checkouts", Counters: map[string]int64{"removed": 1, "retained": 2}})
		})
		if err != nil {
			t.Fatal(err)
		}
		rows := tasks(t, m)
		if len(rows) != 1 || rows[0].Status != Succeeded || rows[0].Schedule.Runs != run || rows[0].Progress.Completed != 3 || rows[0].Progress.Counters["removed"] != 1 || rows[0].Schedule.NextRunAt <= rows[0].FinishedAt {
			t.Fatalf("scheduled receipt: %+v", rows)
		}
		if id != "" && rows[0].ID != id {
			t.Fatal("each poll created another task")
		}
		id = rows[0].ID
	}
}

func TestScheduledWorkDoesNotConsumeExecutionSlotsOrBlockItself(t *testing.T) {
	m, _ := testManager(t, 1)
	err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
		if count, err := m.ActiveCount(ctx); err != nil || count != 1 {
			t.Fatalf("UI count: %d %v", count, err)
		}
		if count, err := m.MaintenanceBlockers(ctx); err != nil || count != 0 {
			t.Fatalf("cleanup blocks itself: %d %v", count, err)
		}
		return m.Do(ctx, Spec{Kind: "child", Title: "Nested bounded work"}, func(ctx context.Context, _ *Reporter) error {
			if count, err := m.MaintenanceBlockers(ctx); err != nil || count != 1 {
				t.Fatalf("real background work did not block maintenance: %d %v", count, err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledWorkRefusesOverlapAndSeesOtherActiveWork(t *testing.T) {
	m, _ := testManager(t, 1)
	err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
		if err := m.ObserveSchedule(ctx, exampleSchedule, func(context.Context) error { t.Fatal("overlapping job ran"); return nil }); !errors.Is(err, ErrBusy) {
			t.Fatalf("overlap: %v", err)
		}
		other := exampleSchedule
		other.Name = "other"
		return m.ObserveSchedule(ctx, other, func(inner context.Context) error {
			if count, err := m.MaintenanceBlockers(inner); err != nil || count != 0 {
				t.Fatalf("observational tasks changed maintenance admission: %d %v", count, err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledFailuresAndPanicsRemainVisible(t *testing.T) {
	for _, mode := range []string{"returned", "reported", "panic"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := testManager(t, 1)
			err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
				switch mode {
				case "returned":
					return errors.New("scan failed")
				case "reported":
					module.ReportError(ctx, errors.New("scan failed"))
				case "panic":
					panic("SECRET panic input")
				}
				return nil
			})
			rows := tasks(t, m)
			if err == nil || len(rows) != 1 || rows[0].Status != Failed || strings.Contains(rows[0].Error, "SECRET") {
				t.Fatalf("failure receipt: %+v %v", rows, err)
			}
			if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if row := tasks(t, m)[0]; row.Status != Succeeded || row.Schedule.Runs != 2 {
				t.Fatalf("failed job did not retry: %+v", row)
			}
		})
	}
}

func TestScheduledStopJoinsCancelledCaller(t *testing.T) {
	m, _ := testManager(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, unblock, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- m.ObserveSchedule(ctx, exampleSchedule, func(ctx context.Context) error { close(started); <-ctx.Done(); <-unblock; return ctx.Err() })
	}()
	<-started
	cancel()
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop did not join the scheduled caller")
	default:
	}
	close(unblock)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	<-stopped
	row := tasks(t, m)[0]
	if row.Status != Cancelled || row.Schedule.NextRunAt != 0 {
		t.Fatalf("cancelled schedule: %+v", row)
	}
}

func TestScheduledRecoveryClearsStaleNextRun(t *testing.T) {
	m, store := testManager(t, 1)
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	m.Stop()
	restarted := New(store, 1)
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	if row := tasks(t, restarted)[0]; row.Schedule.NextRunAt != 0 || row.Schedule.Runs != 1 {
		t.Fatalf("stale next run after restart: %+v", row)
	}
}

func TestScheduledObservationWithoutDatabasePreservesWork(t *testing.T) {
	called := false
	if err := New(nil, 1).ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatalf("unobserved job: %v %v", called, err)
	}
}
