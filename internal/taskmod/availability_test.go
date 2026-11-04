package taskmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBackgroundTasksUnavailableRemoteDoesNotBlockAggregate(t *testing.T) {
	for _, remoteErr := range []error{
		status.Error(codes.Unimplemented, "unknown method ApplicationTasks"),
		status.Error(codes.Unavailable, "disconnected"),
		errors.New("remote task query failed"),
	} {
		t.Run(remoteErr.Error(), func(t *testing.T) {
			manager := tasksvc.New(statetest.Open(t, tasksvc.Schema), 1)
			if err := manager.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.Stop)
			if err := manager.Do(t.Context(), tasksvc.Spec{Kind: "scan", Title: "Local scan"}, func(context.Context, *tasksvc.Reporter) error { return nil }); err != nil {
				t.Fatal(err)
			}
			m := New(manager).UseOwners(func() map[string]tasksvc.Reader {
				return map[string]tasksvc.Reader{
					"healthy": &taskReader{tasks: []tasksvc.Task{{ID: "remote", Status: tasksvc.Running}}},
					"broken":  &taskReader{tasks: []tasksvc.Task{{ID: "partial", Status: tasksvc.Running}}, err: remoteErr},
				}
			})
			handler := moduletest.Mount(m)
			for _, query := range []string{"", "?view=active-count"} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks"+query, nil))
				if w.Code != http.StatusOK {
					t.Errorf("aggregate %s: %d %s", query, w.Code, w.Body)
					continue
				}
				if query != "" {
					if w.Body.String() != "{\"count\":1}\n" {
						t.Errorf("count=%s", w.Body)
					}
					continue
				}
				var tasks []tasksvc.Task
				if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil || len(tasks) != 2 {
					t.Fatalf("tasks=%s err=%v", w.Body, err)
				}
				if tasks[0].OwnerID != "healthy" || tasks[1].OwnerID != "local" {
					t.Errorf("healthy and local work not preserved: %+v", tasks)
				}
			}
			for _, path := range []string{"?remoteId=broken", "?remoteId=broken&view=active-count", "/remote?remoteId=broken"} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks"+path, nil))
				if w.Code != http.StatusServiceUnavailable {
					t.Errorf("explicit read %s: %d %s", path, w.Code, w.Body)
				}
			}
		})
	}
}

func TestBackgroundTasksAggregateStillReportsLocalFailure(t *testing.T) {
	m := New(tasksvc.New(nil, 1)).UseOwners(func() map[string]tasksvc.Reader {
		return map[string]tasksvc.Reader{
			"healthy": &taskReader{tasks: []tasksvc.Task{{ID: "remote", Status: tasksvc.Running}}},
		}
	})
	for _, query := range []string{"", "?view=active-count"} {
		w := httptest.NewRecorder()
		moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks"+query, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("local failure %s: %d %s", query, w.Code, w.Body)
		}
	}
}
