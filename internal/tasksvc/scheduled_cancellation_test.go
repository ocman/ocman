package tasksvc

import (
	"context"
	"errors"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestScheduledReceiptCancellationDuringRead(t *testing.T) {
	m, store := testManager(t, 1)
	old := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(old) })
	hook := logtest.NewGlobal()
	// Hold the only connection so cancellation occurs after admission, while
	// the receipt read is waiting, rather than at the initial caller.Err guard.
	store.SQL().SetMaxOpenConns(1)
	conn, err := store.SQL().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.ObserveSchedule(ctx, exampleSchedule, func(context.Context) error {
			t.Error("maintenance ran after cancellation")
			return nil
		})
	}()
	until := time.Now().Add(5 * time.Second)
	for store.SQL().Stats().WaitCount == 0 {
		if time.Now().After(until) {
			t.Fatal("receipt read did not wait for connection")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	for _, entry := range hook.AllEntries() {
		if entry.Level <= log.WarnLevel {
			t.Errorf("ordinary cancellation warned: %s", entry.Message)
		}
	}
}

func TestScheduledReceiptFailureRemainsWarning(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "storage failure", true: "deadline"}[deadline], func(t *testing.T) {
			m, store := testManager(t, 1)
			old := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
			t.Cleanup(func() { log.StandardLogger().ReplaceHooks(old) })
			hook := logtest.NewGlobal()
			ctx := t.Context()
			called := false
			if deadline {
				store.SQL().SetMaxOpenConns(1)
				conn, err := store.SQL().Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			} else if _, err := store.SQL().Exec("DROP TABLE background_task"); err != nil {
				t.Fatal(err)
			}
			err := m.ObserveSchedule(ctx, exampleSchedule, func(context.Context) error { called = true; return nil })
			if deadline && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("error=%v", err)
			}
			if !deadline && err != nil {
				t.Errorf("maintenance fallback failed: %v", err)
			}
			if called == deadline {
				t.Errorf("deadline=%v maintenance called=%v", deadline, called)
			}
			entry := hook.LastEntry()
			if entry == nil || entry.Level != log.WarnLevel {
				t.Errorf("failure warning missing: %v", entry)
			}
		})
	}
}
