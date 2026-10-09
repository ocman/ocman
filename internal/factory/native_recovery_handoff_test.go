package factory

import (
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestDispatchHandsOffCheckpointedRecovery(t *testing.T) {
	for _, failure := range []string{"", "stop", "checkpoint"} {
		name := failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			db, err := state.Open(statetest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			launcher := &fakeImplementationLauncher{}
			svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
			epic := createPouredWorkEpic(t, svc, "Paused acceptance")
			mol := pouredIssueID(t, svc, epic.ID, "mol")
			proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: mol, Project: "/repo", Nodes: []ManifestNode{
				{Key: "acceptance", Type: "implementation", Requirement: "required", Title: "Acceptance", AcceptanceCriteria: []string{"passes"}},
				{Key: "prerequisite", Type: "implementation", Requirement: "required", Title: "Prerequisite", AcceptanceCriteria: []string{"passes"}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
				t.Fatal(err)
			}
			if len(launcher.calls) != 1 {
				t.Fatalf("initial launches: %#v", launcher.calls)
			}
			first := launcher.calls[0]
			gate, err := svc.CreateRecoveryGate(t.Context(), first.AttemptID, first.AgentToken, "Run prerequisite first", "Acceptance blocked", nil)
			if err != nil {
				t.Fatal(err)
			}
			launcher.result = model.PlanningSession{Platform: "opencode", ID: "prerequisite-session"}
			if failure == "stop" {
				launcher.stopErr = errors.New("cannot stop writer")
			}
			if failure == "checkpoint" {
				launcher.handoffErr = errors.New("factory worktree has uncommitted changes")
			}
			if err := svc.Dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				if len(launcher.calls) != 1 {
					t.Fatal("unsafe recovery launched another writer")
				}
				if failure == "stop" && launcher.handoffs != 0 {
					t.Fatal("validated a checkpoint before stopping the writer")
				}
				return
			}
			if len(launcher.calls) != 2 || launcher.calls[1].Title != "Prerequisite" {
				t.Fatalf("ready prerequisite did not start: %#v", launcher.calls)
			}
			if len(launcher.stops) != 1 || launcher.handoffs != 1 {
				t.Fatalf("handoff: stops=%v validations=%d", launcher.stops, launcher.handoffs)
			}
			if !strings.Contains(strings.Join(launcher.prepared, " "), "/repo:abc123") {
				t.Fatalf("checkpoint not forwarded: %v", launcher.prepared)
			}
			if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue"); err == nil {
				t.Fatal("resumed while prerequisite owns workspace")
			}
			second := launcher.calls[1]
			launcher.checkpointSHA = "prerequisite-head"
			if err := svc.CompleteAttempt(t.Context(), second.AttemptID, second.AgentToken, "Prerequisite fixed", ""); err != nil {
				t.Fatal(err)
			}
			launcher.handoffErr = errors.New("factory handoff does not include the accepted checkpoint")
			if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue"); err == nil || len(launcher.recoveries) != 0 {
				t.Fatal("resumed a workspace that discarded prerequisite progress")
			}
			if launcher.checkpointRefs[len(launcher.checkpointRefs)-1] != "prerequisite-head" {
				t.Fatalf("resume used stale checkpoint: %v", launcher.checkpointRefs)
			}
			launcher.handoffErr = nil
			if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue"); err != nil {
				t.Fatalf("resume after prerequisite: %v", err)
			}
			attempt, found, err := db.GetFactoryAttempt(t.Context(), first.AttemptID)
			if err != nil || !found || attempt.Phase != model.FactoryAttemptActive || attempt.Outcome != "" {
				t.Fatalf("acceptance falsely completed: %#v, %v", attempt, err)
			}
		})
	}
}
