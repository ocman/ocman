package factory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestTransferIssueReservesAndResumes(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Transfer", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []ManifestNode{{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}}, {Key: "second", Type: "implementation", Title: "Second", Requirement: "required", AcceptanceCriteria: []string{"done"}}, {Key: "third", Type: "implementation", Title: "Third", Requirement: "required", AcceptanceCriteria: []string{"done"}}}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: nodes}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	issues, _ := svc.ListIssues(t.Context(), epic.ID)
	for _, issue := range issues {
		ids[issue.Title] = issue.ID
	}
	creates := 0
	create := func(_ context.Context, issue Issue) (string, error) {
		creates++
		return "https://forge.example/issues/" + issue.ID, nil
	}

	// Running work and non-task Issues never reach the tracker.
	running := launcher.prompts[0].WorkID
	if _, err := svc.TransferIssue(t.Context(), epic.ID, running, create); !errors.Is(err, ErrInvalidRequest) || creates != 0 {
		t.Fatalf("running transfer = %v, creates %d", err, creates)
	}
	if _, err := svc.TransferIssue(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "phase"), create); !errors.Is(err, ErrInvalidRequest) || creates != 0 {
		t.Fatalf("phase transfer = %v", err)
	}

	// While the tracker call runs the ticket is out of dispatch; a failed call
	// puts it back.
	failing := func(_ context.Context, issue Issue) (string, error) {
		if got := issueStatus(t, svc, epic.ID, issue.ID); got != "deferred" || issue.Title != "Second" {
			t.Fatalf("reserved issue = %s %q", got, issue.Title)
		}
		return "", errors.New("forge down")
	}
	if _, err := svc.TransferIssue(t.Context(), epic.ID, ids["Second"], failing); err == nil {
		t.Fatal("forge failure succeeded")
	}
	if got := issueStatus(t, svc, epic.ID, ids["Second"]); got != "open" {
		t.Fatalf("released issue = %s", got)
	}

	url, err := svc.TransferIssue(t.Context(), epic.ID, ids["Second"], create)
	if err != nil || url != "https://forge.example/issues/"+ids["Second"] || creates != 1 {
		t.Fatalf("transfer = %q, %v, creates %d", url, err, creates)
	}
	if got := issueStatus(t, svc, epic.ID, ids["Second"]); got != "removed" {
		t.Fatalf("transferred issue = %s", got)
	}
	comments, err := svc.ListIssueComments(t.Context(), epic.ID, ids["Second"])
	if err != nil || len(comments) != 1 || comments[0].Body != "Transferred to "+url {
		t.Fatalf("comments = %#v, %v", comments, err)
	}
	if _, err := svc.TransferIssue(t.Context(), epic.ID, ids["Second"], create); err == nil || creates != 1 {
		t.Fatal("a transferred issue was transferred again")
	}

	// An in-flight or uncertain transfer has one owner: a second request
	// never creates again, and only Cancel releases it.
	_, staleClaim, err := db.ReserveFactoryIssueTransfer(t.Context(), epic.ID, ids["Third"], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TransferIssue(t.Context(), epic.ID, ids["Third"], create); !errors.Is(err, ErrInvalidRequest) || creates != 1 {
		t.Fatalf("concurrent transfer = %v, creates %d", err, creates)
	}
	if err := svc.CancelIssueTransfer(t.Context(), epic.ID, ids["Third"]); err != nil {
		t.Fatal(err)
	}
	if got := issueStatus(t, svc, epic.ID, ids["Third"]); got != "open" {
		t.Fatalf("cancelled transfer = %s", got)
	}
	if err := db.RecordFactoryIssueTransfer(t.Context(), epic.ID, ids["Third"], staleClaim, "https://forge.example/late"); err == nil {
		t.Fatal("a cancelled transfer recorded a tracker issue")
	}

	// Cancel -> re-reserve -> the old call finishes: the old claim can neither
	// record onto nor release the new reservation.
	_, claim, err := db.ReserveFactoryIssueTransfer(t.Context(), epic.ID, ids["Third"], time.Now())
	if err != nil || claim == staleClaim {
		t.Fatalf("re-reserve = %q, %v", claim, err)
	}
	if err := db.RecordFactoryIssueTransfer(t.Context(), epic.ID, ids["Third"], staleClaim, "https://forge.example/late"); err == nil {
		t.Fatal("a stale claim recorded onto a new reservation")
	}
	if err := db.ReleaseFactoryIssueTransfer(t.Context(), epic.ID, ids["Third"], staleClaim); err != nil {
		t.Fatal(err)
	}
	if got := issueStatus(t, svc, epic.ID, ids["Third"]); got != "deferred" {
		t.Fatalf("a stale claim released the new reservation: %s", got)
	}

	// A tracker issue recorded before a local failure is reused on retry.
	if err := db.RecordFactoryIssueTransfer(t.Context(), epic.ID, ids["Third"], claim, "https://forge.example/existing"); err != nil {
		t.Fatal(err)
	}
	if reason := issueReason(t, svc, epic.ID, ids["Third"]); reason != "Transferred to https://forge.example/existing" {
		t.Fatalf("recorded reason = %q", reason)
	}
	if url, err := svc.TransferIssue(t.Context(), epic.ID, ids["Third"], create); err != nil || url != "https://forge.example/existing" || creates != 1 {
		t.Fatalf("resumed transfer = %q, %v, creates %d", url, err, creates)
	}
}

// A live create call cannot be cancelled, and a call that may have reached
// the tracker keeps its reservation for the user to check.
func TestTransferIssueKeepsLiveAndUncertainReservations(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, &fakeImplementationLauncher{store: db})
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Transfer", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []ManifestNode{{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}}, {Key: "second", Type: "implementation", Title: "Second", Requirement: "required", AcceptanceCriteria: []string{"done"}}}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: nodes, Edges: []ManifestEdge{{From: "second", To: "first", Type: "blocks"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	var second string
	issues, _ := svc.ListIssues(t.Context(), epic.ID)
	for _, issue := range issues {
		if issue.Title == "Second" {
			second = issue.ID
		}
	}
	uncertain := func(ctx context.Context, issue Issue) (string, error) {
		if err := svc.CancelIssueTransfer(ctx, epic.ID, issue.ID); err == nil {
			t.Error("cancelled a live transfer")
		}
		return "", fmt.Errorf("timeout: %w", ErrTrackerUncertain)
	}
	if _, err := svc.TransferIssue(t.Context(), epic.ID, second, uncertain); !errors.Is(err, ErrTrackerUncertain) {
		t.Fatalf("uncertain transfer = %v", err)
	}
	if got := issueStatus(t, svc, epic.ID, second); got != "deferred" {
		t.Fatalf("uncertain transfer released the ticket: %s", got)
	}
	if err := svc.CancelIssueTransfer(t.Context(), epic.ID, second); err != nil {
		t.Fatal(err)
	}
	if got := issueStatus(t, svc, epic.ID, second); got != "open" {
		t.Fatalf("cancelled transfer = %s", got)
	}
}

func issueStatus(t *testing.T, svc *NativeService, epicID, issueID string) string {
	t.Helper()
	issues, err := svc.ListIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == issueID {
			return issue.Status
		}
	}
	return "removed"
}

func issueReason(t *testing.T, svc *NativeService, epicID, issueID string) string {
	t.Helper()
	issues, err := svc.ListIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == issueID {
			return issue.OutcomeReason
		}
	}
	return ""
}
