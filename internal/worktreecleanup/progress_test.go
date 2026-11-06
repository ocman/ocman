package worktreecleanup

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/tasksvc"
)

func TestCleanupTaskReportsRemovedAndRetainedCheckouts(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(fmt.Sprint(retain), func(t *testing.T) {
			f := newFixture(t, tasksvc.Schema)
			tasks := tasksvc.New(f.store, 1)
			if err := tasks.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer tasks.Stop()
			f.m.d.Idle = func(ctx context.Context) error {
				count, err := tasks.MaintenanceBlockers(ctx)
				if err == nil && count > 0 {
					return fmt.Errorf("other work is pending")
				}
				return err
			}
			f.h.beforeRemove = func(ctx context.Context, req hostsvc.CleanupWorktreeRequest) error {
				_, release, err := f.m.AdmitCleanup(ctx, req)
				if err == nil {
					release()
				}
				return err
			}
			if retain {
				f.h.removeErr = fmt.Errorf("checkout has local edits")
			}
			ctx := module.WithScheduleObserver(t.Context(), tasks)
			err := module.RunScheduled(ctx, module.Schedule{Name: "worktree-cleanup", Title: "Clean archived worktrees", Interval: time.Hour}, f.m.Sweep)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := tasks.List(t.Context(), "", "schedule.worktree-cleanup", 10)
			if err != nil || len(rows) != 1 {
				t.Fatalf("cleanup task: %+v %v", rows, err)
			}
			progress := rows[0].Progress
			if progress.Completed != 1 || progress.Total == nil || *progress.Total != 1 {
				t.Fatalf("unknown cleanup progress: %+v", progress)
			}
			if retain {
				if progress.Counters["retained"] != 1 || progress.Counters["removed"] != 0 {
					t.Fatalf("retention hidden: %+v", progress)
				}
			} else if progress.Counters["removed"] != 1 || len(f.h.removed) != 1 {
				t.Fatalf("task blocked its own cleanup: %+v", progress)
			}
		})
	}
}
