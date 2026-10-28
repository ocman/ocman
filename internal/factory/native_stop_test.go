package factory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/factory/model"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestStopEpicExitsRecoveryWithoutDelivery(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "done", ""); err != nil {
		t.Fatal(err)
	}
	second := next(1)
	gate, err := svc.CreateRecoveryGate(t.Context(), second.AttemptID, second.AgentToken, "Again?", "Loop", nil)
	if err != nil {
		t.Fatal(err)
	}
	launcher.handoffErr = errors.New("dirty workspace")
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil {
		t.Fatal("expected delivery to refuse the dirty workspace")
	}
	svc.ReconcileAttention(t.Context())
	store := svc.store.(*state.DB)
	if err := svc.StopEpic(t.Context(), epicID); err != nil {
		t.Fatal(err)
	}
	ep, err := svc.GetWorkEpic(t.Context(), epicID)
	if err != nil || ep.Status != "closed" {
		t.Fatalf("Epic = %#v, %v", ep, err)
	}
	issues, err := svc.ListIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == first.WorkID && issue.Outcome != "succeeded" {
			t.Fatal("completed work lost")
		}
		if issue.ID == second.WorkID && (issue.Outcome != "cancelled" || !strings.Contains(issue.OutcomeReason, "stopped by user")) {
			t.Fatalf("unfinished work = %#v", issue)
		}
		if issue.Recovery != nil && issue.Recovery.IssueID == gate.IssueID && issue.Recovery.Resolution != "cancel" {
			t.Fatalf("recovery = %#v", issue.Recovery)
		}
	}
	attempts, err := store.ListFactoryAttempts(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		if attempt.Phase != model.FactoryAttemptTerminal {
			t.Fatalf("live attempt after stop: %#v", attempt)
		}
	}
	items, err := store.ListInboxItems(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("stale Inbox = %#v, %v", items, err)
	}
	before := len(launcher.prompts)
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != before {
		t.Fatal("stop launched more work")
	}
	if err := svc.CompleteAttempt(t.Context(), second.AttemptID, second.AgentToken, "late success", ""); err == nil {
		t.Fatal("cancelled worker completed")
	}
	if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "continue"); err == nil {
		t.Fatal("cancelled recovery resumed")
	}
}

func TestStopEpicFailureLeavesPausedWorkRecoverable(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	next(0)
	launcher.stopErr = errors.New("offline")
	if err := svc.StopEpic(t.Context(), epicID); err == nil {
		t.Fatal("stop failure hidden")
	}
	ep, err := svc.GetWorkEpic(t.Context(), epicID)
	if err != nil || ep.Status != "paused" {
		t.Fatalf("Epic = %#v, %v", ep, err)
	}
	launcher.stopErr = nil
	if err := svc.StopEpic(t.Context(), epicID); err != nil {
		t.Fatal(err)
	}
}

type stopDuringLaunch struct {
	*fakeImplementationLauncher
	svc    *NativeService
	epicID string
}

func (l stopDuringLaunch) LaunchImplementationSession(ctx context.Context, req ImplementationSessionRequest) (PlanningSession, error) {
	if err := l.svc.StopEpic(ctx, l.epicID); err != nil {
		return PlanningSession{}, err
	}
	return PlanningSession{Platform: "opencode", ID: "late-worker"}, nil
}

func TestStopEpicFencesALateWorkerLaunch(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "done", ""); err != nil {
		t.Fatal(err)
	}
	svc.implementation = stopDuringLaunch{launcher, svc, epicID}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 1 || len(launcher.stops) == 0 {
		t.Fatalf("late worker prompted: %#v; stops %#v", launcher.prompts, launcher.stops)
	}
	ep, err := svc.GetWorkEpic(t.Context(), epicID)
	if err != nil || ep.Status != "closed" {
		t.Fatalf("Epic = %#v, %v", ep, err)
	}
}

func TestStopEpicUnavailable(t *testing.T) {
	if err := NewNativeWithExecution(&nativeStoreFake{}, nil, nil, nil).StopEpic(t.Context(), "epic"); !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("unavailable stop = %v", err)
	}
}

func TestStopEpicBeforePlanApproval(t *testing.T) {
	store := statetest.Open(t)
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "opencode", ID: "planner"}}
	svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, launcher, nil)
	epic := createPouredWorkEpic(t, svc, "Stop planning")
	if _, err := svc.ClaimPlan(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err != nil {
		t.Fatal(err)
	}
	if err := svc.StopEpic(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stops) != 1 || launcher.stops[0].ID != "planner" {
		t.Fatalf("planning stops = %#v", launcher.stops)
	}
}
