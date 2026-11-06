package autoarchive

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
)

func TestScheduledProjectArchiveReportsHandledPersistenceErrors(t *testing.T) {
	for _, mode := range []string{"archive", "unarchive-query"} {
		t.Run(mode, func(t *testing.T) {
			store := statetest.Open(t, tasksvc.Schema)
			manager := tasksvc.New(store, 1)
			if err := manager.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer manager.Stop()
			host := &projectHost{}
			if mode == "archive" {
				host.projects = []db.ProjectStats{{Directory: "/projects/old", LastUsed: daysAgo(30)}}
				if _, err := store.SQL().Exec(`CREATE TRIGGER reject_project_archive BEFORE INSERT ON archived_project BEGIN SELECT RAISE(FAIL,'project archive unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := store.SQL().Exec(`DROP TABLE unarchived_entity`); err != nil {
				t.Fatal(err)
			}
			m := newModule(store, host)
			err := manager.ObserveSchedule(t.Context(), module.Schedule{Name: "auto-archive", Title: "Archive inactive projects", Interval: time.Hour}, func(ctx context.Context) error { m.ArchiveProjects(ctx); return nil })
			rows, listErr := manager.List(t.Context(), "", "", 10)
			if err == nil || listErr != nil || len(rows) != 1 || rows[0].Status != tasksvc.Failed {
				t.Fatalf("handled failure hidden: rows=%+v err=%v list=%v", rows, err, listErr)
			}
		})
	}
}
