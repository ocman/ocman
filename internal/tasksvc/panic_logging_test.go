package tasksvc

import (
	"context"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestTaskPanicsLogStackWithoutInput(t *testing.T) {
	for _, mode := range []string{"task", "schedule", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			m, db := testManager(t, 1)
			oldHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
			t.Cleanup(func() { log.StandardLogger().ReplaceHooks(oldHooks) })
			hook := logtest.NewGlobal()
			var err error
			if mode == "task" {
				err = m.Do(t.Context(), Spec{Kind: "diagnostic-test", Title: "SECRET title", Key: "SECRET key", Scope: "SECRET scope"}, func(context.Context, *Reporter) error {
					panic("SECRET panic")
				})
			} else {
				if mode == "fallback" {
					if _, err := db.SQL().Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON background_task BEGIN SELECT RAISE(FAIL,'receipt unavailable'); END`); err != nil {
						t.Fatal(err)
					}
				}
				err = m.ObserveSchedule(t.Context(), exampleSchedule, func(context.Context) error { panic("SECRET panic") })
			}
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe or missing panic error: %v", err)
			}
			var panics []*log.Entry
			for _, e := range hook.AllEntries() {
				text, err := e.String()
				if err != nil || strings.Contains(text, "SECRET") {
					t.Errorf("input leaked into logs: %q %v", text, err)
				}
				if e.Level == log.ErrorLevel {
					panics = append(panics, e)
				}
			}
			if len(panics) != 1 {
				t.Fatalf("want one panic diagnostic, got %v", panics)
			}
			e := panics[0]
			stack, _ := e.Data["stack"].(string)
			if !strings.Contains(stack, "panic_logging_test.go:") || !strings.Contains(stack, "runtime/panic.go:") {
				t.Errorf("panic call site missing: %q", stack)
			}
			if mode == "task" {
				if e.Data["task_id"] != tasks(t, m)[0].ID || e.Data["kind"] != "diagnostic-test" {
					t.Errorf("task identity missing: %v", e.Data)
				}
			} else if e.Data["job"] != exampleSchedule.Name {
				t.Errorf("schedule identity missing: %v", e.Data)
			}
			if mode != "fallback" {
				row := tasks(t, m)[0]
				if row.Status != Failed || strings.Contains(row.Error, "SECRET") || strings.Contains(row.Error, "goroutine") {
					t.Errorf("panic receipt changed: %+v", row)
				}
			}
		})
	}
}
