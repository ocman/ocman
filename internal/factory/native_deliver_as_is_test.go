package factory

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func deliverAsIsFixture(t *testing.T) (*NativeService, *fakeImplementationLauncher, string, func(int) ImplementationSessionRequest) {
	t.Helper()
	db := statetest.Open(t)
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Deliver as is", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeliverAsIs(t.Context(), epic.ID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("delivered before any plan: %v", err)
	}
	approveProposal(t, svc, SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{
		{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}},
		{Key: "second", Type: "implementation", Title: "Second", Requirement: "required", AcceptanceCriteria: []string{"done"}},
	}, Edges: []ManifestEdge{{From: "second", To: "first", Type: "blocks"}}}})
	return svc, launcher, epic.ID, func(index int) ImplementationSessionRequest {
		t.Helper()
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("step %d: %#v", index, launcher.prompts)
		}
		return launcher.prompts[index]
	}
}

// The review loop: a validator proposes yet another follow-up. Delivering as
// is stops the validator, accepts and leaves out the follow-up, waives
// verification, and runs delivery listing what was left out.
func TestDeliverAsIsEndsTheReviewLoop(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil || !strings.Contains(err.Error(), "wait for running work") {
		t.Fatalf("delivered while implementation runs: %v", err)
	}
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	second := next(1)
	if err := svc.CompleteAttempt(t.Context(), second.AttemptID, second.AgentToken, "second done", ""); err != nil {
		t.Fatal(err)
	}
	verification := next(2)
	if !verification.Verification {
		t.Fatal("verification did not start")
	}
	issues, err := svc.ListIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	var phase string
	for _, issue := range issues {
		if issue.Kind == "phase" {
			phase = issue.ID
		}
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "create", EpicID: epicID, ParentID: phase, Kind: "task", Title: "Yet another gap"}); err != nil {
		t.Fatal(err)
	}
	stops := len(launcher.stops)
	leftOut, err := svc.DeliverAsIs(t.Context(), epicID)
	if err != nil || len(leftOut) != 1 {
		t.Fatalf("deliver as is = %v, %v", leftOut, err)
	}
	if len(launcher.stops) != stops+1 {
		t.Fatal("the running validator was not stopped")
	}
	if err := svc.CompleteAttempt(t.Context(), verification.AttemptID, verification.AgentToken, "late verdict", ""); err == nil {
		t.Fatal("a stopped validator completed")
	}
	delivery := next(3)
	if !delivery.Delivery || !strings.Contains(delivery.LeftOut, "Yet another gap") || strings.Contains(delivery.LeftOut, "Second") {
		t.Fatalf("delivery = %#v", delivery)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epicID)
	if err != nil || detail.PlanGate.Resolution != "approved" {
		t.Fatalf("plan gate = %#v, %v", detail.PlanGate, err)
	}
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil {
		t.Fatal("delivered as is while delivery runs")
	}
}

// Work paused for a human decision is also a way out: the paused Issue is left
// out and the never-run verification is waived.
func TestDeliverAsIsLeavesOutWorkPausedForRecovery(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	second := next(1)
	gate, err := svc.CreateRecoveryGate(t.Context(), second.AttemptID, second.AgentToken, "Which API?", "Both change behaviour", nil)
	if err != nil {
		t.Fatal(err)
	}
	leftOut, err := svc.DeliverAsIs(t.Context(), epicID)
	if err != nil || len(leftOut) != 1 {
		t.Fatalf("deliver as is = %v, %v", leftOut, err)
	}
	if delivery := next(2); !delivery.Delivery || !strings.Contains(delivery.LeftOut, "Second") {
		t.Fatalf("delivery = %#v", delivery)
	}
	if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Use A"); err == nil {
		t.Fatal("the paused attempt's recovery gate stayed open")
	}
	if len(launcher.stops) == 0 {
		t.Fatal("the paused session was not stopped")
	}
}

// Delivering as is retires every decision the paused attempt waited on, and
// refuses (keeping the decision) when the paused workspace is not handed off.
func TestDeliverAsIsRetiresGatesAndChecksTheWorkspace(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	next(1)
	attempts, err := svc.store.ListFactoryAttempts(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	var session string
	for _, attempt := range attempts {
		if attempt.Phase != "terminal" {
			session = attempt.Session.ID
		}
	}
	gate, handled, err := svc.EscalatePermission(t.Context(), session, "request", "external_directory", "/outside")
	if err != nil || !handled {
		t.Fatalf("authority gate = %v, %v", handled, err)
	}

	launcher.handoffErr = errors.New("uncommitted changes")
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil || !strings.Contains(err.Error(), "not a clean, pushed checkpoint") {
		t.Fatalf("dirty workspace = %v", err)
	}
	if len(launcher.stops) != 0 {
		t.Fatal("a refused delivery stopped the paused session")
	}
	launcher.handoffErr, launcher.checkpointSHA = nil, "newer"
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil || !strings.Contains(err.Error(), "beyond the accepted checkpoint") {
		t.Fatalf("unaccepted commits = %v", err)
	}

	launcher.checkpointSHA = ""
	leftOut, err := svc.DeliverAsIs(t.Context(), epicID)
	if err != nil || len(leftOut) != 1 {
		t.Fatalf("deliver as is = %v, %v", leftOut, err)
	}
	if _, err := svc.ResolveAuthorityEscalationGate(t.Context(), gate.IssueID, "approve"); err == nil {
		t.Fatal("the cancelled attempt's authority gate stayed open")
	}
	issues, err := svc.ListIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == gate.IssueID && (issue.Status != "closed" || issue.Outcome != "cancelled") {
			t.Fatalf("authority gate issue = %#v", issue)
		}
	}
}

// With nothing left out the result is an empty list, never null.
func TestDeliverAsIsWithNothingLeftOut(t *testing.T) {
	svc, _, epicID, next := deliverAsIsFixture(t)
	for i := range 2 {
		work := next(i)
		if err := svc.CompleteAttempt(t.Context(), work.AttemptID, work.AgentToken, "done", ""); err != nil {
			t.Fatal(err)
		}
	}
	next(2) // verification
	leftOut, err := svc.DeliverAsIs(t.Context(), epicID)
	if err != nil || leftOut == nil || len(leftOut) != 0 {
		t.Fatalf("deliver as is = %#v, %v", leftOut, err)
	}
	if delivery := next(3); !delivery.Delivery || delivery.LeftOut != "" {
		t.Fatalf("delivery = %#v", delivery)
	}
}

// A recovery handoff advances the accepted checkpoint past the one the paused
// attempt started from; delivering as is accepts the newer one.
func TestDeliverAsIsAcceptsARecoveryCheckpoint(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	second := next(1)
	gate, err := svc.CreateRecoveryGate(t.Context(), second.AttemptID, second.AgentToken, "Which API?", "Both change behaviour", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := svc.store.(*state.DB)
	if err := db.RecordFactoryRecoveryCheckpoint(t.Context(), gate.IssueID, model.FactoryAttemptResult{Branch: second.Branch, CommitSHA: "handoff", TargetBranch: second.TargetBranch}, time.Now()); err != nil {
		t.Fatal(err)
	}
	launcher.checkpointSHA = "handoff"
	if leftOut, err := svc.DeliverAsIs(t.Context(), epicID); err != nil || len(leftOut) != 1 {
		t.Fatalf("deliver as is = %v, %v", leftOut, err)
	}
	if got := launcher.checkpointRefs[len(launcher.checkpointRefs)-1]; got != "handoff" {
		t.Fatalf("validated against %q, want the recovery checkpoint", got)
	}
}

// A decision still being carried out refuses delivery before anything stops.
func TestDeliverAsIsWaitsForAPendingDecision(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	next(1)
	attempts, err := svc.store.ListFactoryAttempts(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	var session string
	for _, attempt := range attempts {
		if attempt.Phase != "terminal" {
			session = attempt.Session.ID
		}
	}
	gate, _, err := svc.EscalatePermission(t.Context(), session, "request", "external_directory", "/outside")
	if err != nil {
		t.Fatal(err)
	}
	launcher.replyErr = errors.New("permission reply lost")
	if _, err := svc.ResolveAuthorityEscalationGate(t.Context(), gate.IssueID, "approve"); err == nil {
		t.Fatal("approval delivery did not fail")
	}
	stops := len(launcher.stops)
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil || !strings.Contains(err.Error(), "still being carried out") {
		t.Fatalf("pending decision = %v", err)
	}
	if len(launcher.stops) != stops {
		t.Fatal("a refused delivery stopped a session")
	}
}
