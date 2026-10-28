package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/factory/model"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type rebasedCheckpointLauncher struct {
	fakeImplementationLauncher
	rebased bool
	head    string
	err     error
}

func (l *rebasedCheckpointLauncher) ValidateImplementationCheckpoint(ctx context.Context, repo, branch, checkpoint string) (string, error) {
	if !l.rebased {
		return l.fakeImplementationLauncher.ValidateImplementationCheckpoint(ctx, repo, branch, checkpoint)
	}
	if checkpoint != "" {
		return "", errors.New("factory handoff does not include the accepted checkpoint")
	}
	return l.head, l.err
}

func checkpointRecoveryFixture(t *testing.T) (*NativeService, *state.DB, *rebasedCheckpointLauncher, ImplementationSessionRequest, model.FactoryAttempt) {
	t.Helper()
	db := statetest.Open(t)
	l := &rebasedCheckpointLauncher{head: strings.Repeat("b", 40)}
	l.checkpointSHA = strings.Repeat("a", 40)
	s := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, l)
	approvedEpic(t, s, "Rebased checkpoint", []ManifestNode{
		{Key: "first", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}},
		{Key: "rebase", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}, DependsOn: []string{"first"}},
	}...)
	first := l.calls[0]
	if err := s.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := l.calls[1]
	attempt, found, err := db.GetFactoryAttempt(t.Context(), run.AttemptID)
	if err != nil || !found {
		t.Fatalf("attempt missing: %v", err)
	}
	l.rebased = true
	return s, db, l, run, attempt
}

func TestRebasedCheckpointRequiresExactHumanRecovery(t *testing.T) {
	for _, scenario := range []string{"approved", "unapproved", "ordinary response", "wrong attempt", "wrong branch", "wrong old SHA", "wrong new SHA", "moved head", "dirty worktree"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, l, run, attempt := checkpointRecoveryFixture(t)
			policy := attempt.FrozenPolicy
			id, branch, old, head := attempt.ID, policy.Branch, policy.CheckpointSHA, l.head
			switch scenario {
			case "wrong attempt":
				id += "-other"
			case "wrong branch":
				branch += "-other"
			case "wrong old SHA":
				old = strings.Repeat("c", 40)
			case "wrong new SHA":
				head = strings.Repeat("c", 40)
			}
			response := fmt.Sprintf("Accept rebased Factory checkpoint: attempt=%q repository=%q branch=%q old=%s new=%s", id, policy.Repository, branch, old, head)
			if scenario == "ordinary response" {
				response = "Allow the rebase and force push with lease"
			}
			if scenario != "unapproved" {
				var gate RecoveryGate
				var err error
				if scenario == "approved" {
					gate, err = s.RequestCheckpointRecovery(t.Context(), run.AttemptID, run.AgentToken)
					if err == nil && (len(gate.Choices) != 1 || gate.Choices[0] != response) {
						t.Fatalf("recovery choice does not bind exact checkpoint: %+v", gate)
					}
				} else {
					gate, err = s.CreateRecoveryGate(t.Context(), run.AttemptID, run.AgentToken, "Accept rebase?", "Authorized rewrite", []string{response})
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", response); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "moved head" {
				l.head = strings.Repeat("c", 40)
			}
			if scenario == "dirty worktree" {
				l.err = errors.New("factory worktree has uncommitted changes")
			}
			err := s.CompleteAttempt(t.Context(), run.AttemptID, run.AgentToken, "rebased", "")
			if (err == nil) != (scenario == "approved") {
				t.Fatalf("completion error = %v", err)
			}
			saved, _, readErr := db.GetFactoryAttempt(t.Context(), attempt.ID)
			if readErr != nil || saved.FrozenPolicy.CheckpointSHA != policy.CheckpointSHA {
				t.Fatalf("original checkpoint was not retained: %+v, %v", saved, readErr)
			}
			if scenario == "approved" && (saved.Result == nil || saved.Result.CommitSHA != l.head || saved.Outcome != model.FactoryAttemptSucceeded) {
				t.Fatalf("rebased checkpoint was not accepted: %+v", saved)
			}
		})
	}
}

func TestRequestCheckpointRecoveryFailsClosed(t *testing.T) {
	for _, scenario := range []string{"bad token", "intact ancestry", "dirty worktree", "unavailable launcher"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, l, run, _ := checkpointRecoveryFixture(t)
			switch scenario {
			case "bad token":
				run.AgentToken = "bad"
			case "intact ancestry":
				l.rebased = false
			case "dirty worktree":
				l.err = errors.New("factory worktree has uncommitted changes")
			case "unavailable launcher":
				s.implementation = nil
			}
			if _, err := s.RequestCheckpointRecovery(t.Context(), run.AttemptID, run.AgentToken); err == nil {
				t.Fatal("unsafe checkpoint recovery was offered")
			}
		})
	}
}

func rebasedDeliveryFixture(t *testing.T) (*NativeService, *state.DB, *rebasedCheckpointLauncher, ImplementationSessionRequest) {
	t.Helper()
	db := statetest.Open(t)
	l := &rebasedCheckpointLauncher{head: strings.Repeat("b", 40)}
	l.checkpointSHA = strings.Repeat("a", 40)
	s := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, l)
	approvedEpic(t, s, "Rebased delivery", []ManifestNode{
		{Key: "only", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}},
	}...)
	if err := s.CompleteAttempt(t.Context(), l.calls[0].AttemptID, l.calls[0].AgentToken, "only", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	delivery := l.calls[len(l.calls)-1]
	if !delivery.Delivery {
		t.Fatalf("delivery was not dispatched: %#v", delivery)
	}
	l.rebased = true
	return s, db, l, delivery
}

func TestRebasedDeliveryCheckpoint(t *testing.T) {
	const prURL = "https://forge.example/pr/1"
	for _, scenario := range []string{"unapproved", "exact approval", "force-complete"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, l, run := rebasedDeliveryFixture(t)
			var gate RecoveryGate
			var err error
			switch scenario {
			case "exact approval":
				gate, err = s.RequestCheckpointRecovery(t.Context(), run.AttemptID, run.AgentToken)
			case "force-complete":
				gate, err = s.CreateRecoveryGate(t.Context(), run.AttemptID, run.AgentToken, "Merged", "PR merged after rebase", []string{"Force-complete the delivery attempt using merged PR #1"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(gate.Choices) == 1 {
				if _, err := s.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", gate.Choices[0]); err != nil {
					t.Fatal(err)
				}
			}
			err = s.CompleteAttempt(t.Context(), run.AttemptID, run.AgentToken, "delivered", prURL)
			if (err == nil) != (scenario != "unapproved") {
				t.Fatalf("delivery completion error = %v", err)
			}
			saved, _, readErr := db.GetFactoryAttempt(t.Context(), run.AttemptID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario != "unapproved" && (saved.Result == nil || saved.Result.CommitSHA != l.head) {
				t.Fatalf("rebased delivery was not recorded: %+v", saved)
			}
		})
	}
}
