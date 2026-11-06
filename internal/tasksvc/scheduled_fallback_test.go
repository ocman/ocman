package tasksvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/module"
)

func TestScheduledReceiptStorageFailureDoesNotSkipMaintenance(t *testing.T) {
	for _, mode := range []string{"lookup", "insert"} {
		t.Run(mode, func(t *testing.T) {
			m, db := testManager(t, 1)
			if mode == "lookup" {
				if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if _, err := db.SQL().Exec(`UPDATE background_task SET data=json_set(data,'$.schedule.runs','invalid')`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := db.SQL().Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON background_task BEGIN SELECT RAISE(FAIL,'receipt storage unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			calls := 0
			want := errors.New("maintenance failed independently")
			err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
				calls++
				if err := module.ReportProgress(ctx, Progress{Completed: -1}); err != nil {
					t.Errorf("fallback unexpectedly attached a reporter: %v", err)
				}
				if err := m.ObserveSchedule(ctx, exampleSchedule, func(context.Context) error { t.Error("overlapping fallback ran"); return nil }); !errors.Is(err, ErrBusy) {
					t.Errorf("fallback overlap: %v", err)
				}
				return want
			})
			if calls != 1 || !errors.Is(err, want) {
				t.Fatalf("maintenance calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestScheduledFallbackIsJoinedAndRecoversPanics(t *testing.T) {
	m, db := testManager(t, 1)
	if _, err := db.SQL().Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON background_task BEGIN SELECT RAISE(FAIL,'receipt storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { panic("private input") }); err == nil || err.Error() != "scheduled task panicked" {
		t.Fatalf("fallback panic: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- m.ObserveSchedule(ctx, exampleSchedule, func(ctx context.Context) error { close(started); <-release; return ctx.Err() })
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("maintenance skipped: %v", err)
	}
	cancel()
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Error("Stop did not join fallback")
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-stopped
}
