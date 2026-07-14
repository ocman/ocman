package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/loops"
)

type messengerFunc func(context.Context, string, string, string, string, string) error

func (f messengerFunc) SendPrompt(ctx context.Context, sessionID, prompt, model, agent, reasoning string) error {
	return f(ctx, sessionID, prompt, model, agent, reasoning)
}

// newLoopsTestServer builds a Server with an in-memory state DB and a
// loop service whose messenger is a no-op (no platform registry needed).
func newLoopsTestServer(t *testing.T) *Server {
	t.Helper()
	sdb := openWatcherTestStateDB(t)
	srv := &Server{stateDB: sdb}
	prev := loopServiceFn
	loopServiceFn = func(s *Server) *loops.Service {
		return loops.NewService(loops.Deps{
			Store:     sdb,
			Messenger: messengerFunc(func(context.Context, string, string, string, string, string) error { return nil }),
		})
	}
	t.Cleanup(func() { loopServiceFn = prev })
	return srv
}

func TestHandleLoops_CreateListGet(t *testing.T) {
	srv := newLoopsTestServer(t)

	body := `{
		"root_session_id": "sess1",
		"directory": "/src/ocman",
		"trigger_type": "schedule",
		"trigger_config": {"interval_seconds": 120},
		"action_type": "prompt_root",
		"action_template": "heartbeat",
		"stop_conditions": {"max_iterations": 10, "max_cost_usd": 2}
	}`
	rec := httptest.NewRecorder()
	srv.handleLoops(rec, httptest.NewRequest(http.MethodPost, "/api/loops", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var created loops.LoopView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.State != loops.StateActive {
		t.Fatalf("unexpected created loop: %+v", created)
	}

	// List.
	rec = httptest.NewRecorder()
	srv.handleLoops(rec, httptest.NewRequest(http.MethodGet, "/api/loops?session=sess1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}
	var list []loops.LoopView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 loop, got %d", len(list))
	}

	// Get detail.
	rec = httptest.NewRecorder()
	srv.handleLoops(rec, httptest.NewRequest(http.MethodGet, "/api/loops/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", rec.Code)
	}
}

func TestHandleLoops_CreateRejectsNoBudget(t *testing.T) {
	srv := newLoopsTestServer(t)
	body := `{"root_session_id":"s1","trigger_type":"schedule","action_type":"prompt_root","stop_conditions":{"max_iterations":10}}`
	rec := httptest.NewRecorder()
	srv.handleLoops(rec, httptest.NewRequest(http.MethodPost, "/api/loops", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing budget, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleLoops_PauseResumeDelete(t *testing.T) {
	srv := newLoopsTestServer(t)
	view, err := srv.loopSvc().Create(context.Background(), loops.LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    loops.TriggerSchedule,
		TriggerConfig:  loops.TriggerConfig{IntervalSeconds: 60},
		ActionType:     loops.ActionPromptRoot,
		StopConditions: loops.StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, action := range []string{"pause", "resume"} {
		rec := httptest.NewRecorder()
		srv.handleLoops(rec, httptest.NewRequest(http.MethodPost, "/api/loops/"+view.ID+"/"+action, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d (%s)", action, rec.Code, rec.Body.String())
		}
	}

	// DELETE /api/loops/{id} soft-deletes.
	rec := httptest.NewRecorder()
	srv.handleLoops(rec, httptest.NewRequest(http.MethodDelete, "/api/loops/"+view.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	l, _ := srv.stateDB.GetLoop(view.ID)
	if l.State != loops.StateDeleted {
		t.Fatalf("expected deleted after delete, got %s", l.State)
	}
}
