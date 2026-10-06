package factory

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestAmendmentCannotDeliverBeforeFreshVerificationWithoutReconciliation(t *testing.T) {
	for _, finishBeforeApproval := range []bool{true, false} {
		t.Run(map[bool]string{true: "already verified", false: "old validator finishes after approval"}[finishBeforeApproval], func(t *testing.T) {
			db, err := state.Open(statetest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			launcher := &fakeImplementationLauncher{store: db}
			svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
			epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Fresh verification fence", InitialProject: "/repo", AcknowledgeLocalExecution: true})
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{
				{Key: "work", Title: "Required", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}},
				{Key: "optional", Title: "Optional", Type: "implementation", Requirement: "optional", AcceptanceCriteria: []string{"done"}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
				t.Fatal(err)
			}
			issues, err := svc.ListIssues(t.Context(), epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			var optional string
			for _, issue := range issues {
				if issue.Title == "Optional" {
					optional = issue.ID
				}
			}
			if optional == "" {
				t.Fatal("optional work missing")
			}
			if err := svc.DeferIssue(t.Context(), epic.ID, optional, "Deferred"); err != nil {
				t.Fatal(err)
			}
			if err := svc.Dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			work := launcher.prompts[0]
			if err := svc.CompleteAttempt(t.Context(), work.AttemptID, work.AgentToken, "work done", ""); err != nil {
				t.Fatal(err)
			}
			if err := svc.Dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			oldVerification := launcher.prompts[1]
			if !oldVerification.Verification {
				t.Fatal("validator missing")
			}
			finish := func() {
				if err := svc.CompleteAttempt(t.Context(), oldVerification.AttemptID, oldVerification.AgentToken, "old graph verified", ""); err != nil {
					t.Fatal(err)
				}
			}
			if finishBeforeApproval {
				finish()
			}
			if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "edit", EpicID: epic.ID, IssueID: optional, Title: "Updated optional work"}); err != nil {
				t.Fatal(err)
			}
			// Model the dispatch that reconciled before approval committed.
			if err := db.EnsureFactoryDeliveryIssue(t.Context(), epic.ID); err != nil {
				t.Fatal(err)
			}
			gate, err := db.GetFactoryPlanGate(t.Context(), epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.DecideFactoryPlanGate(t.Context(), epic.ID, "approve", gate.ProposalRevision, gate.ProposalHash, ""); err != nil {
				t.Fatal(err)
			}
			if !finishBeforeApproval {
				finish()
			} else {
				issues, err := db.ListFactoryIssues(t.Context(), epic.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, issue := range issues {
					if issue.ID == oldVerification.WorkID && (issue.Status != "open" || issue.Outcome != "") {
						t.Fatalf("approval did not invalidate verification atomically: %#v", issue)
					}
				}
			}
			delivery := pouredIssueID(t, svc, epic.ID, "delivery")
			// No Ensure/Dispatch between approval (or old completion) and claim.
			if _, _, err := db.ClaimFactoryImplementation(t.Context(), epic.ID, delivery, "factory-implement/v1", time.Now()); err == nil {
				t.Fatal("Delivery claimed an unverified amendment")
			}
			if err := svc.Dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(launcher.prompts) != 3 || !launcher.prompts[2].Verification {
				t.Fatalf("fresh validator did not run: %#v", launcher.prompts)
			}
			fresh := launcher.prompts[2]
			if err := svc.CompleteAttempt(t.Context(), fresh.AttemptID, fresh.AgentToken, "new graph verified", ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.ClaimFactoryImplementation(t.Context(), epic.ID, delivery, "factory-implement/v1", time.Now()); err != nil {
				t.Fatalf("fresh verification did not release Delivery: %v", err)
			}
		})
	}
}
