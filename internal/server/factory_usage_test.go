package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type usagePlatform struct {
	fakePlatform
	usage map[string]map[string]platforms.Usage
	err   error
}

func TestRemovedVerificationKeepsUsagePhase(t *testing.T) {
	raw, err := sql.Open("sqlite", t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ep, err := store.CreateFactoryEpic(t.Context(), "e", "Usage", "", "/repo", "usage", model.NativeFormula{ID: "usage", Version: 1, Hash: "hash", Nodes: []model.NativeFormulaNode{{Key: "verify", Kind: "task", Workflow: &model.WorkflowStep{Kind: "verification"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, issues, err := store.PourFactoryEpic(t.Context(), ep.ID, model.NativeFormula{ID: "usage", Version: 1, Hash: "hash", Nodes: []model.NativeFormulaNode{{Key: "verify", Kind: "task", Workflow: &model.WorkflowStep{Kind: "verification"}}}})
	if err != nil {
		t.Fatal(err)
	}
	workID := ""
	for _, issue := range issues {
		if issue.Workflow != nil && issue.Workflow.Kind == "verification" {
			workID = issue.ID
		}
	}
	if workID == "" {
		t.Fatal("missing verification fixture")
	}
	attempt, err := store.CreatePreparedFactoryAttempt(t.Context(), ep.ID, workID, model.FactoryAttemptPolicy{Profile: "factory-implement/v1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateFactoryAttempt(t.Context(), attempt.ID, model.PlanningSession{Platform: "p", ID: "verify"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO factory_removed_issue(issue_id, plan_id, plan_revision, removed_at) VALUES (?, ?, 0, 1)`, workID, ep.ID); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, store, "", nil, nil)
	srv.factory = &fakeFactoryService{epics: []factory.WorkEpic{{ID: ep.ID}}}
	srv.registry.Register(&usagePlatform{fakePlatform: fakePlatform{id: "p"}, usage: map[string]map[string]platforms.Usage{"verify": {"verify": {Cost: 1}}}})
	w := httptest.NewRecorder()
	srv.handleFactoryEpic(w, httptest.NewRequest(http.MethodGet, "/api/factory/epics/"+ep.ID+"/usage", nil))
	var got factoryEpicUsage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	if got.Phases["verify"].Cost != 1 || got.Phases["implement"].Cost != 0 {
		t.Fatalf("removed verification usage = %+v", got.Phases)
	}
}

func (p *usagePlatform) SessionUsage(_ context.Context, id string) (map[string]platforms.Usage, error) {
	return p.usage[id], p.err
}

func TestFactoryUsagePhasesRetriesAndDedup(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	u := platforms.Usage{Tokens: platforms.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}, Cost: 0.25, EstCost: 0.5}
	p := &usagePlatform{fakePlatform: fakePlatform{id: "owner"}, usage: map[string]map[string]platforms.Usage{
		"plan": {"plan": u, "child": u}, "retry": {"retry": u}, "impl": {"impl": u}, "verify": {"verify": u}, "deliver": {"deliver": u},
	}}
	srv.registry.Register(p)
	other := &usagePlatform{fakePlatform: fakePlatform{id: "other"}, usage: map[string]map[string]platforms.Usage{"plan": {"plan": u}}}
	srv.registry.Register(other)
	attempts := []model.FactoryAttempt{}
	for _, id := range []string{"plan", "retry", "impl", "verify", "deliver", "plan"} {
		attempt := model.FactoryAttempt{ID: id, WorkID: id, Session: model.PlanningSession{Platform: "owner", ID: id}}
		if id == "plan" || id == "retry" {
			attempt.FrozenPolicy.Profile = "factory-plan/v1"
		}
		attempts = append(attempts, attempt)
	}
	attempts = append(attempts, model.FactoryAttempt{ID: "other", Session: model.PlanningSession{Platform: "other", ID: "plan"}}, model.FactoryAttempt{ID: "not-launched"})
	issues := []model.NativeIssue{{ID: "verify", Workflow: &model.WorkflowStep{Kind: "verification"}}, {ID: "deliver", Kind: "delivery"}}
	got := srv.factoryUsage(t.Context(), attempts, issues)
	if got.Incomplete || len(got.Attempts) != 8 || got.Total.Cost != 1.75 || got.Total.EstCost != 3.5 || got.Total.Tokens.CacheRead != 21 {
		t.Fatalf("usage = %+v", got)
	}
	for stage, cost := range map[string]float64{"plan": 0.75, "implement": 0.5, "verify": 0.25, "deliver": 0.25} {
		if got.Phases[stage].Cost != cost {
			t.Fatalf("%s = %+v", stage, got.Phases[stage])
		}
	}
	if got.Attempts[0].Usage.Cost != 0.5 || got.Attempts[7].Usage == nil {
		t.Fatalf("attempts = %+v", got.Attempts)
	}
	// Delivery can still be classified from frozen policy when its issue is absent.
	attempts[0].FrozenPolicy.Profile = ""
	attempts[0].FrozenPolicy.Delivery = true
	if got := srv.factoryUsage(t.Context(), attempts[:1], nil); got.Phases["deliver"].Cost != 0.5 {
		t.Fatal(got)
	}
}

func TestFactoryUsageUnavailable(t *testing.T) {
	for _, kind := range []string{"missing", "unsupported", "failed"} {
		t.Run(kind, func(t *testing.T) {
			srv := New(nil, nil, "", nil, nil)
			if kind == "unsupported" {
				srv.registry.Register(&fakePlatform{id: "p"})
			}
			if kind == "failed" {
				srv.registry.Register(&usagePlatform{fakePlatform: fakePlatform{id: "p"}, err: errors.New("offline")})
			}
			got := srv.factoryUsage(t.Context(), []model.FactoryAttempt{{Session: model.PlanningSession{Platform: "p", ID: "session"}}}, nil)
			if !got.Incomplete || got.Attempts[0].Usage != nil {
				t.Fatalf("usage = %+v", got)
			}
		})
	}
}

func TestFactoryUsageRoute(t *testing.T) {
	srv := New(nil, openTestStateDB(t), "", nil, nil)
	if _, err := srv.stateDB.CreateFactoryEpic(t.Context(), "e", "Usage", "", "/repo", "usage", model.NativeFormula{ID: "usage", Version: 1, Hash: "hash", Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}); err != nil {
		t.Fatal(err)
	}
	srv.factory = &fakeFactoryService{epics: []factory.WorkEpic{{ID: "e"}}}
	request := httptest.NewRequest(http.MethodGet, "/api/factory/epics/e/usage", nil)
	w := httptest.NewRecorder()
	srv.handleFactoryEpic(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var got factoryEpicUsage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Phases) != 4 || got.Attempts == nil {
		t.Fatal(got)
	}
	srv.factory = &fakeFactoryService{err: factory.ErrWorkEpicNotFound}
	w = httptest.NewRecorder()
	srv.handleFactoryEpic(w, request)
	if w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
	srv.factory = &fakeFactoryService{epics: []factory.WorkEpic{{ID: "e"}}}
	srv.stateDB = nil
	w = httptest.NewRecorder()
	srv.handleFactoryEpic(w, request)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	srv.handleFactoryEpic(w, httptest.NewRequest(http.MethodPost, request.URL.String(), nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}
