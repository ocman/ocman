package factory

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type recoveryWakeStore struct {
	*state.DB
	settled chan struct{}
}

func (s *recoveryWakeStore) NextFactoryDispatchAt(ctx context.Context, interval time.Duration) (time.Time, error) {
	next, err := s.DB.NextFactoryDispatchAt(ctx, interval)
	select {
	case s.settled <- struct{}{}:
	default:
	}
	return next, err
}

func TestRecoveryCancellationDispatchesNextWorkWithoutIdleEvent(t *testing.T) {
	d, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := &recoveryWakeStore{DB: d, settled: make(chan struct{}, 1)}
	launcher := &fakeImplementationLauncher{store: d}
	svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	ep := createPouredWorkEpic(t, svc, "Cancel optional work")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: ep.ID, Manifest: ProposalManifest{EpicID: ep.ID, MolID: pouredIssueID(t, svc, ep.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{
		{Key: "optional", Type: "implementation", Requirement: "optional", AcceptanceCriteria: []string{"done"}},
		{Key: "required", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), ep.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("initial launches = %d", len(launcher.calls))
	}
	first := launcher.calls[0]
	gate, err := svc.CreateRecoveryGate(t.Context(), first.AttemptID, first.AgentToken, "Cannot continue", "Need guidance", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Drain setup wakes, then let the startup reconciliation settle while the
	// optional session is paused. Cancellation must produce the only new wake.
	for len(svc.dispatchWake) > 0 {
		<-svc.dispatchWake
	}
	launcher.launched = make(chan struct{}, 1)
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	// Instrumented SQLite work can take several seconds on a loaded CI runner.
	const dispatchTimeout = 10 * time.Second
	select {
	case <-store.settled:
	case <-time.After(dispatchTimeout):
		t.Fatal("startup dispatch did not settle")
	}
	if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "cancel", "Skip optional work"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-launcher.launched:
	case <-time.After(dispatchTimeout):
		t.Fatal("cancellation did not dispatch ready work without another idle event")
	}
	svc.Close() // Wait for activation before inspecting the launcher.
	if len(launcher.calls) != 2 || launcher.calls[1].WorkID == first.WorkID {
		t.Fatalf("launches = %#v", launcher.calls)
	}
}
