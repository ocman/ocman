package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type sessionTasksResponse struct {
	Tasks map[string]struct {
		Status   db.SessionStatus `json:"status"`
		Messages json.RawMessage  `json:"messages"`
		Parts    json.RawMessage  `json:"parts"`
	} `json:"tasks"`
	Children json.RawMessage `json:"children"`
}

func getSessionTasks(t *testing.T, s *Server, path string) sessionTasksResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	serveRoutes(t, s, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body sessionTasksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestHandleSessionTasks_ReturnsTaskDataWithoutStateDB(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{
		sessions: []db.Session{{ID: "task-1"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{Status: db.StatusBusy}}, nil
		},
	})
	s := New(nil, nil, "127.0.0.1:0", reg, nil)

	body := getSessionTasks(t, s, "/api/session/parent-1/tasks?ids=task-1")

	task, ok := body.Tasks["task-1"]
	if !ok {
		t.Fatalf("tasks = %+v", body.Tasks)
	}
	if string(task.Messages) != "[]" || string(task.Parts) != "[]" {
		t.Fatalf("task = %+v", task)
	}
	if task.Status != db.StatusBusy {
		t.Fatalf("task status = %q, want busy", task.Status)
	}
	if body.Children != nil {
		t.Fatalf("response includes retired children field: %s", body.Children)
	}
}

func TestHandleSessionTasks_AllowsMissingSessionHeader(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{
		sessions: []db.Session{{ID: "task-1"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{}, nil
		},
	})
	s := New(nil, nil, "127.0.0.1:0", reg, nil)
	body := getSessionTasks(t, s, "/api/session/parent-1/tasks?ids=task-1")
	task, ok := body.Tasks["task-1"]
	if !ok || task.Status != "" || string(task.Messages) != "[]" || string(task.Parts) != "[]" {
		t.Fatalf("task = %+v, found = %v", task, ok)
	}
}
