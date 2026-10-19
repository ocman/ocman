package tasksvc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProgressValidation(t *testing.T) {
	negative, total := int64(-1), int64(2)
	for name, progress := range map[string]Progress{
		"negative total":   {Total: &negative},
		"over total":       {Completed: 3, Total: &total},
		"long phase":       {Phase: strings.Repeat("x", 129)},
		"long unit":        {Unit: strings.Repeat("x", 65)},
		"long counter":     {Counters: map[string]int64{strings.Repeat("x", 65): 1}},
		"negative counter": {Counters: map[string]int64{"failed": -1}},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := testManager(t, 1)
			err := m.Do(t.Context(), Spec{Kind: "test", Title: name}, func(_ context.Context, r *Reporter) error {
				return r.Report(progress)
			})
			if !errors.Is(err, ErrProgress) {
				t.Fatalf("invalid progress accepted: %v", err)
			}
		})
	}
}

func TestProgressCannotRegressWithinPhaseOrReportAfterCompletion(t *testing.T) {
	m, _ := testManager(t, 1)
	var reporter *Reporter
	err := m.Do(t.Context(), Spec{Kind: "test", Title: "Progress"}, func(_ context.Context, r *Reporter) error {
		reporter = r
		if err := r.Report(Progress{Phase: "copy", Completed: 2}); err != nil {
			return err
		}
		return r.Report(Progress{Phase: "copy", Completed: 1})
	})
	if !errors.Is(err, ErrProgress) {
		t.Fatal(err)
	}
	if err := reporter.Report(Progress{}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestActiveTasksAppearBeforeNewerTerminalRecords(t *testing.T) {
	m, _ := testManager(t, 1)
	for _, task := range []Task{
		{ID: "running", Status: Running, UpdatedAt: 1},
		{ID: "queued", Status: Queued, UpdatedAt: 2},
		{ID: "done", Status: Succeeded, UpdatedAt: 999},
	} {
		if err := m.store.insert(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.List(t.Context(), "", "", 2)
	if err != nil || len(list) != 2 || list[0].ID != "running" || list[1].ID != "queued" {
		t.Fatalf("active tasks hidden by history: %+v %v", list, err)
	}
}

func TestCallerCancellationStopsAndJoinsWork(t *testing.T) {
	m, _ := testManager(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	entered, exited := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.Do(ctx, Spec{Kind: "test", Title: "Cancellable"}, func(ctx context.Context, _ *Reporter) error {
			close(entered)
			<-ctx.Done()
			close(exited)
			return ctx.Err()
		})
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Do returned before work exited")
	}
	if task := tasks(t, m)[0]; task.Status != Cancelled {
		t.Fatalf("cancel receipt: %+v", task)
	}
}
