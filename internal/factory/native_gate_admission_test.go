package factory

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestFactoryGateCreationAdmitsAnotherEpicWithoutIdleEvent(t *testing.T) {
	for _, kind := range []string{"recovery", "project", "authority"} {
		t.Run(kind, func(t *testing.T) {
			d, err := state.Open(statetest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.Close() })
			if err := d.SetFactoryCapacityPolicy(t.Context(), model.FactoryCapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1}); err != nil {
				t.Fatal(err)
			}
			store := &recoveryWakeStore{DB: d, settled: make(chan struct{}, 1)}
			launcher := &fakeImplementationLauncher{store: d}
			svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
			for _, goal := range []string{"Running", "Waiting"} {
				ep := createPouredWorkEpic(t, svc, goal)
				proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: ep.ID, Manifest: ProposalManifest{EpicID: ep.ID, MolID: pouredIssueID(t, svc, ep.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.DecidePlanGate(t.Context(), ep.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
					t.Fatal(err)
				}
			}
			if len(launcher.calls) != 1 {
				t.Fatalf("initial launches = %d", len(launcher.calls))
			}
			first := launcher.calls[0]
			attempt, found, err := d.GetFactoryAttempt(t.Context(), first.AttemptID)
			if err != nil || !found {
				t.Fatalf("active attempt = %#v, %v", attempt, err)
			}
			for len(svc.dispatchWake) > 0 {
				<-svc.dispatchWake
			}
			launcher.launched = make(chan struct{}, 1)
			if err := svc.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(svc.Close)
			select {
			case <-store.settled:
			case <-time.After(10 * time.Second):
				t.Fatal("startup dispatch did not settle")
			}
			switch kind {
			case "recovery":
				_, err = svc.CreateRecoveryGate(t.Context(), first.AttemptID, first.AgentToken, "Need guidance", "Cannot continue", nil)
			case "project":
				_, err = svc.RequestProject(t.Context(), first.AttemptID, first.AgentToken, "/other", "Need another project")
			case "authority":
				if _, handled, err := svc.EscalatePermission(t.Context(), attempt.Session.ID, "ordinary-request", "bash", "git status"); err != nil || handled {
					t.Fatalf("ordinary permission = %v, %v", handled, err)
				}
				if len(svc.dispatchWake) != 0 {
					t.Fatal("ordinary permission woke dispatch without freeing capacity")
				}
				var handled bool
				_, handled, err = svc.EscalatePermission(t.Context(), attempt.Session.ID, "request", "external_directory", "/outside")
				if err == nil && !handled {
					t.Fatal("out-of-profile permission did not create a gate")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-launcher.launched:
			case <-time.After(10 * time.Second):
				t.Fatal("gate creation did not admit the waiting Epic without an idle event")
			}
			svc.Close()
			if len(launcher.calls) != 2 || launcher.calls[1].EpicID == first.EpicID {
				t.Fatalf("launches = %#v", launcher.calls)
			}
		})
	}
}
