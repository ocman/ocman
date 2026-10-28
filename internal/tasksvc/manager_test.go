package tasksvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func testManager(t *testing.T, concurrency int) (*Manager, *state.DB) {
	t.Helper()
	d := statetest.Open(t, Schema)
	m := New(d, concurrency)
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	return m, d
}

func tasks(t *testing.T, m *Manager) []Task {
	t.Helper()
	list, err := m.List(t.Context(), "", "", 200)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestDoPersistsProgressAndTerminalResult(t *testing.T) {
	m, _ := testManager(t, 2)
	total := int64(3)
	err := m.Do(t.Context(), Spec{Kind: "test", Key: "source", Title: "Example", Scope: "resource"}, func(ctx context.Context, r *Reporter) error {
		p := Progress{Phase: "import", Total: &total, Unit: "sessions", Counters: map[string]int64{"imported": 0}}
		for i := int64(0); i <= total; i++ {
			p.Completed = i
			p.Counters["imported"] = i
			if err := r.Report(p); err != nil {
				return err
			}
		}
		p.Counters["imported"] = 999 // reporting must snapshot caller-owned maps
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	list := tasks(t, m)
	if len(list) != 1 || list[0].Status != Succeeded || list[0].Progress.Completed != 3 || list[0].Progress.Counters["imported"] != 3 || list[0].FinishedAt == 0 || list[0].StartedAt == 0 || list[0].OwnerID != "local" {
		t.Fatalf("task: %+v", list)
	}
	got, err := m.Get(t.Context(), list[0].ID)
	if err != nil || got.ID != list[0].ID {
		t.Fatalf("detail: %+v %v", got, err)
	}
	if _, err := m.Get(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	filtered, err := m.List(t.Context(), Succeeded, "test", 1)
	if err != nil || len(filtered) != 1 {
		t.Fatalf("filtered tasks: %+v %v", filtered, err)
	}
	filtered, err = m.List(t.Context(), Failed, "test", 1)
	if err != nil || len(filtered) != 0 {
		t.Fatal("status filter ignored")
	}
}

func TestFailureAndPanicAreNotSuccess(t *testing.T) {
	for name, work := range map[string]Work{
		"failure": func(context.Context, *Reporter) error { return errors.New("failed") },
		"panic":   func(context.Context, *Reporter) error { panic("sensitive input") },
		"incomplete": func(_ context.Context, r *Reporter) error {
			total := int64(3)
			return r.Report(Progress{Phase: "work", Completed: 1, Total: &total})
		},
		"invalid ignored": func(_ context.Context, r *Reporter) error { _ = r.Report(Progress{Completed: -1}); return nil },
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := testManager(t, 1)
			if err := m.Do(t.Context(), Spec{Kind: "test", Title: name}, work); err == nil {
				t.Fatal("reported failure as successful")
			}
			task := tasks(t, m)[0]
			if task.Status != Failed || task.Error == "" || strings.Contains(task.Error, "sensitive input") {
				t.Fatalf("failure receipt: %+v", task)
			}
		})
	}
}

func TestBoundedExecutionAndActiveKeyDeduplication(t *testing.T) {
	m, _ := testManager(t, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	first, err := m.Submit(t.Context(), Spec{Kind: "test", Key: "one", Title: "First"}, func(context.Context, *Reporter) error { close(entered); <-release; return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	first.Progress.Counters["mutated"] = 9
	if _, err := m.Submit(t.Context(), Spec{Kind: "test", Key: "one", Title: "Duplicate"}, func(context.Context, *Reporter) error { return nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate admitted: %v", err)
	}
	var secondCalled atomic.Bool
	second, err := m.Submit(t.Context(), Spec{Kind: "test", Key: "two", Title: "Second"}, func(context.Context, *Reporter) error { secondCalled.Store(true); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != Queued || secondCalled.Load() {
		t.Fatal("concurrency bound ignored")
	}
	close(release)
	for deadline := time.Now().Add(time.Second); ; {
		got, err := m.Get(t.Context(), second.ID)
		if err == nil && got.Status == Succeeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued work never ran")
		}
		time.Sleep(time.Millisecond)
	}
	if !secondCalled.Load() {
		t.Fatal("work missing")
	}
	if err := m.Do(t.Context(), Spec{Kind: "test", Key: "one", Title: "Next"}, func(context.Context, *Reporter) error { return nil }); err != nil {
		t.Fatal("terminal task kept key reserved", err)
	}
}

func TestCancellationJoinsWorkAndRefusesNewSubmissions(t *testing.T) {
	m, _ := testManager(t, 1)
	entered, exited := make(chan struct{}), make(chan struct{})
	_, err := m.Submit(t.Context(), Spec{Kind: "test", Title: "Running"}, func(ctx context.Context, _ *Reporter) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	var queuedCalled atomic.Bool
	_, err = m.Submit(t.Context(), Spec{Kind: "test", Title: "Queued"}, func(context.Context, *Reporter) error { queuedCalled.Store(true); return nil })
	if err != nil {
		t.Fatal(err)
	}
	m.Stop()
	select {
	case <-exited:
	default:
		t.Fatal("shutdown did not join work")
	}
	if queuedCalled.Load() {
		t.Fatal("queued callback ran after shutdown cancellation")
	}
	for _, task := range tasks(t, m) {
		if task.Status != Cancelled {
			t.Fatalf("shutdown task: %+v", task)
		}
	}
	if err := m.Do(t.Context(), Spec{Kind: "test", Title: "Late"}, func(context.Context, *Reporter) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRecoveryInterruptsRatherThanReplaysAndBoundsRetention(t *testing.T) {
	m, d := testManager(t, 1)
	for _, status := range []Status{Queued, Running, Succeeded} {
		task := Task{ID: string(status), Spec: Spec{Kind: "old", Title: "Old"}, Status: status, Progress: Progress{}, CreatedAt: 1, UpdatedAt: 1}
		if err := m.store.insert(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	m.Stop()
	restarted := New(d, 1)
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	for _, id := range []string{"queued", "running"} {
		task, err := restarted.Get(t.Context(), id)
		if err != nil || task.Status != Interrupted || task.FinishedAt == 0 || !strings.Contains(task.Error, "not replayed") {
			t.Fatalf("recovery: %+v %v", task, err)
		}
	}
	if task, err := restarted.Get(t.Context(), "succeeded"); err != nil || task.Status != Succeeded {
		t.Fatal("recovered a terminal task")
	}
	for i := range 1005 {
		if err := restarted.store.insert(t.Context(), Task{ID: fmt.Sprint(i), Spec: Spec{Kind: "old", Title: "Old"}, Status: Succeeded, UpdatedAt: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := restarted.store.prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.SQL().QueryRow(`SELECT count(*) FROM background_task`).Scan(&count); err != nil || count != 1000 {
		t.Fatalf("history retention: %d %v", count, err)
	}
}

func TestValidationAndUnavailableDatabase(t *testing.T) {
	m, _ := testManager(t, 1)
	for _, spec := range []Spec{{}, {Kind: "test"}, {Title: "title"}, {Kind: strings.Repeat("k", 129), Title: "title"}, {Kind: "test", Title: strings.Repeat("t", 257)}} {
		if _, err := m.Submit(t.Context(), spec, func(context.Context, *Reporter) error { return nil }); err == nil {
			t.Fatal("accepted invalid specification")
		}
	}
	if _, err := m.Submit(t.Context(), Spec{Kind: "test", Title: "nil"}, nil); err == nil {
		t.Fatal("accepted nil work")
	}
	for _, limit := range []int{0, 201} {
		if _, err := m.List(t.Context(), "", "", limit); err == nil {
			t.Fatal("invalid limit")
		}
	}
	if _, err := m.List(t.Context(), "invalid", "", 1); err == nil {
		t.Fatal("invalid status")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.Submit(ctx, Spec{Kind: "test", Title: "cancelled"}, func(context.Context, *Reporter) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unavailable := New(nil, 0)
	if err := unavailable.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := unavailable.List(t.Context(), "", "", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := unavailable.Get(t.Context(), "id"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := unavailable.Do(t.Context(), Spec{}, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	unavailable.Stop()
}
