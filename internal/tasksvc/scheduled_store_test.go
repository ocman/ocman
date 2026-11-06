package tasksvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module"
)

func TestStopScheduleClearsHintAfterCallerCancellation(t *testing.T) {
	m, _ := testManager(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	ctx = module.WithScheduleObserver(ctx, m)
	spec := exampleSchedule
	spec.Scope = "Webhook inbox public-id"
	if err := module.RunScheduled(ctx, spec, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	cancel()
	module.StopSchedule(ctx, spec.Name)
	row := tasks(t, m)[0]
	if row.Schedule.NextRunAt != 0 || row.Scope != spec.Scope {
		t.Fatalf("stopped schedule: %+v", row)
	}
	if err := m.StopSchedule(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	m.Stop()
	if err := m.StopSchedule(ctx, spec.Name); err != nil {
		t.Fatal(err)
	}
	if err := New(nil, 1).StopSchedule(ctx, spec.Name); err != nil {
		t.Fatal(err)
	}
}

func TestScheduledRetentionAndListPriority(t *testing.T) {
	m, _ := testManager(t, 1)
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduled := tasks(t, m)[0]
	// A live loop can be delayed beyond its approximate hint.
	scheduled.Schedule.NextRunAt = time.Now().Add(-time.Hour).UnixMilli()
	if err := m.store.save(t.Context(), scheduled); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1002; i++ {
		row := Task{ID: fmt.Sprint(i), Spec: Spec{Kind: "ordinary", Title: "Completed work"}, Status: Succeeded, UpdatedAt: scheduled.UpdatedAt + time.Hour.Milliseconds() + int64(i+1)}
		if err := m.store.insert(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.store.prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := m.List(t.Context(), "", "", 1)
	if err != nil || len(rows) != 1 || rows[0].ID != scheduled.ID {
		t.Fatalf("schedule lost behind recent history: %+v %v", rows, err)
	}
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	resumed, err := m.Get(t.Context(), scheduled.ID)
	if err != nil || resumed.Schedule.Runs != 2 || resumed.CreatedAt != scheduled.CreatedAt {
		t.Fatalf("live schedule history reset: %+v %v", resumed, err)
	}
	if err := m.StopSchedule(t.Context(), exampleSchedule.Name); err != nil {
		t.Fatal(err)
	}
	if err := m.store.prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(t.Context(), scheduled.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stopped receipt never expired: %v", err)
	}
}

func TestScheduledValidationAndAdmission(t *testing.T) {
	m, _ := testManager(t, 1)
	for _, spec := range []module.Schedule{
		{}, {Name: "name", Title: "title"}, {Name: "name", Title: "title", Interval: -time.Second},
		{Name: strings.Repeat("n", 120), Title: "title", Interval: time.Second},
		{Name: "name", Title: strings.Repeat("t", 257), Interval: time.Second},
		{Name: "name", Title: "title", Scope: strings.Repeat("s", 513), Interval: time.Second},
	} {
		if err := m.ObserveSchedule(t.Context(), spec, func(context.Context) error { t.Fatal("invalid job ran"); return nil }); err == nil {
			t.Fatalf("accepted %+v", spec)
		}
	}
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, nil); err == nil {
		t.Fatal("accepted nil work")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.ObserveSchedule(ctx, exampleSchedule, func(context.Context) error { t.Fatal("cancelled work ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	m.Stop()
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { t.Fatal("closed manager ran work"); return nil }); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := New(nil, 1).MaintenanceBlockers(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestScheduledReporterRejectsLateUpdatesAndClonesMetadata(t *testing.T) {
	m, _ := testManager(t, 1)
	var captured context.Context
	err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
		captured = ctx
		return module.ReportProgress(ctx, Progress{Completed: -1})
	})
	if !errors.Is(err, ErrProgress) {
		t.Fatal(err)
	}
	module.ReportError(captured, errors.New("late subscription failure"))
	if err := module.ReportProgress(captured, Progress{}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	row := tasks(t, m)[0]
	if strings.Contains(row.Error, "late subscription") {
		t.Fatal(row.Error)
	}
	cloned := cloneTask(row)
	cloned.Schedule.Runs++
	if cloned.Schedule.Runs == row.Schedule.Runs {
		t.Fatal("schedule metadata aliased")
	}
}
