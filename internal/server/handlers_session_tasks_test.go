package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

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
	req := httptest.NewRequest(http.MethodGet, "/api/session/parent-1/tasks", nil)
	rec := httptest.NewRecorder()

	s.dispatchSessionSubpath(rec, req)

	var body struct {
		Children []struct {
			ID        string `json:"id"`
			Intent    string `json:"intent"`
			Status    string `json:"status"`
			CreatedAt int64  `json:"createdAt"`
		} `json:"children"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Children) != 1 || body.Children[0].ID != "child-early-link" || body.Children[0].Intent != "Explain the recent work" {
		t.Fatalf("children = %+v", body.Children)
	}
}
