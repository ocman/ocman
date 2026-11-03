package factorymod

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
)

type disabledImplementation struct {
	factory.ImplementationLauncher
	entered chan context.Context
}

func (l disabledImplementation) PrepareImplementationWorkspace(ctx context.Context, _ string, _ string, _ string, _ string) (string, string, error) {
	if l.entered != nil {
		l.entered <- ctx
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	return "main", "", nil
}

func (disabledImplementation) LaunchImplementationSession(context.Context, factory.ImplementationSessionRequest) (factory.PlanningSession, error) {
	return factory.PlanningSession{}, errors.New("unexpected disabled launch")
}

func TestDisabledFactoryRESTMutationDoesNotDispatch(t *testing.T) {
	db := statetest.Open(t)
	svc := factory.NewNativeWithExecution(db, projectResolver(func(_ context.Context, path string) (string, error) { return path, nil }), nil, disabledImplementation{})
	t.Cleanup(svc.Close)
	m := NewModule(Deps{Service: svc, Enabled: func(context.Context) bool { return false }})
	if err := m.Launch(t.Context()); err != nil {
		t.Fatal(err)
	}
	epic := readyFactoryEpic(t, svc)
	mux, _ := routeMux(t, svc)
	for _, action := range []string{"pause", "resume"} {
		if rec := moduletest.Do(mux, http.MethodPost, "/api/factory/epics/"+epic.ID+"/"+action, "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("%s = %d: %s", action, rec.Code, rec.Body.String())
		}
	}
	if attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID); err != nil || len(attempts) != 0 {
		t.Fatalf("disabled REST mutation dispatched: %v, %v", attempts, err)
	}
}

func readyFactoryEpic(t *testing.T, svc *factory.NativeService) factory.WorkEpic {
	t.Helper()
	epic, err := svc.CreateWorkEpic(t.Context(), factory.CreateWorkEpicRequest{Goal: "Disabled REST dispatch", InitialProject: "/repo", AcknowledgeLocalExecution: true, FormulaID: "ocman/tracer", FormulaRevision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var mol string
	for _, issue := range issues {
		if issue.Kind == "mol" {
			mol = issue.ID
		}
	}
	if err := svc.MutateGraph(t.Context(), factory.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Ready work"}); err != nil {
		t.Fatal(err)
	}
	return epic
}

func TestModuleCloseCancelsConcurrentDisable(t *testing.T) {
	db := statetest.Open(t)
	launcher := disabledImplementation{entered: make(chan context.Context, 1)}
	svc := factory.NewNativeWithExecution(db, projectResolver(func(_ context.Context, path string) (string, error) { return path, nil }), PlanningLauncher{}, launcher)
	t.Cleanup(svc.Close)
	readyFactoryEpic(t, svc)
	enabled := true
	m := NewModule(Deps{Service: svc, Enabled: func(context.Context) bool { return enabled }})
	if err := m.Launch(t.Context()); err != nil {
		t.Fatal(err)
	}
	var launchCtx context.Context
	select {
	case launchCtx = <-launcher.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("launch did not start")
	}
	enabled = false
	disabled := make(chan struct{})
	go func() { defer close(disabled); _ = m.SyncEnabled(t.Context()) }()
	// Synchronize with SyncEnabled holding lifeMu while joining the launch.
	deadline := time.Now().Add(time.Second)
	for m.lifeMu.TryLock() {
		m.lifeMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("disable did not enter the lifecycle lock")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { defer close(closed); m.Close() }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		// Cancel directly so the failing version leaves no goroutines.
		svc.Close()
		<-disabled
		<-closed
		t.Fatal("module shutdown blocked behind disable before canceling admitted work")
	}
	if launchCtx.Err() == nil {
		t.Fatal("shutdown did not cancel the admitted launch")
	}
	select {
	case <-disabled:
	case <-time.After(time.Second):
		t.Fatal("disable did not finish after shutdown")
	}
}
