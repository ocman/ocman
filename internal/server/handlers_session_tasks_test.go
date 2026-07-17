package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type sessionTasksResponse struct {
	Tasks map[string]struct {
		Messages json.RawMessage `json:"messages"`
		Parts    json.RawMessage `json:"parts"`
	} `json:"tasks"`
	Children []struct {
		ID        string `json:"id"`
		Intent    string `json:"intent"`
		Status    string `json:"status"`
		CreatedAt int64  `json:"createdAt"`
	} `json:"children"`
}

func getSessionTasks(t *testing.T, s *Server, path string) sessionTasksResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.dispatchSessionSubpath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body sessionTasksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestHandleSessionTasks_ReturnsMCPChildrenWithoutTaskIDs(t *testing.T) {
	sdb := openWatcherTestStateDB(t)
	if err := sdb.InsertChildSession(state.ChildSession{
		ID:              "child-early-link",
		Platform:        "opencode",
		ParentSessionID: "parent-1",
		Intent:          "Explain the recent work",
		Status:          "running",
		CreatedAt:       1234,
	}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, sdb, "127.0.0.1:0", nil, nil)
	body := getSessionTasks(t, s, "/api/session/parent-1/tasks")
	if len(body.Children) != 1 || body.Children[0].ID != "child-early-link" || body.Children[0].Intent != "Explain the recent work" {
		t.Fatalf("children = %+v", body.Children)
	}
}

func TestHandleSessionTasks_ReturnsTaskDataWithoutStateDB(t *testing.T) {
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
	if !ok {
		t.Fatalf("tasks = %+v", body.Tasks)
	}
	if string(task.Messages) != "[]" || string(task.Parts) != "[]" {
		t.Fatalf("task = %+v", task)
	}
	if len(body.Children) != 0 {
		t.Fatalf("children = %+v", body.Children)
	}
}

func TestHandleSessionTasks_IgnoresChildLookupFailure(t *testing.T) {
	sdb := openWatcherTestStateDB(t)
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}
	s := New(nil, sdb, "127.0.0.1:0", nil, nil)

	body := getSessionTasks(t, s, "/api/session/parent-1/tasks")

	if len(body.Children) != 0 {
		t.Fatalf("children = %+v", body.Children)
	}
}
