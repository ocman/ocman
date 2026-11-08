package tasksvc

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/ocman/ocman/internal/state/statetest"
)

func TestScheduledWorkRetriesAfterCompletionWriteFailure(t *testing.T) {
	m, db := testManager(t, 1)
	if _, err := db.SQL().Exec(`CREATE TRIGGER reject_completion BEFORE UPDATE ON background_task
		WHEN NEW.status <> 'running' BEGIN SELECT RAISE(FAIL,'completion write failed'); END`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	work := func(context.Context) error { calls++; return nil }
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, work); err == nil {
		t.Fatal("completion write did not fail")
	}
	row := tasks(t, m)[0]
	if row.Status != Running || row.Progress.Phase != "starting" || calls != 1 {
		t.Fatalf("expected completed work with a stale receipt: %+v calls=%d", row, calls)
	}
	if _, err := db.SQL().Exec(`DROP TRIGGER reject_completion`); err != nil {
		t.Fatal(err)
	}
	if err := m.ObserveSchedule(t.Context(), exampleSchedule, work); err != nil {
		t.Fatalf("recovered storage still blocks maintenance: %v", err)
	}
	row = tasks(t, m)[0]
	if calls != 2 || row.Status != Succeeded || row.Schedule.Runs != 2 {
		t.Fatalf("maintenance did not resume without restart: %+v calls=%d", row, calls)
	}
}

func TestScheduledWorkRetriesAfterCompletionDeadline(t *testing.T) {
	db := statetest.Open(t, Schema)
	synctest.Test(t, func(t *testing.T) {
		m := New(db, 1)
		if err := m.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer m.Stop()
		var held *sql.Conn
		defer func() {
			if held != nil {
				_ = held.Close()
			}
		}()
		err := m.ObserveSchedule(t.Context(), exampleSchedule, func(ctx context.Context) error {
			var err error
			held, err = db.SQL().Conn(ctx)
			return err
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected receipt deadline: %v", err)
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		held = nil
		if err := m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("completion deadline permanently blocked maintenance: %v", err)
		}
	})
}
