package factory

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

type failingActivationStore struct{ *state.DB }

func (failingActivationStore) ActivateFactoryAttempt(context.Context, string, PlanningSession, time.Time) (bool, error) {
	return false, errors.New("write failed")
}

type materializationNotFoundStore struct{ *state.DB }

func (materializationNotFoundStore) MaterializeFactoryPlan(context.Context, string, string, string, time.Time) (model.NativeMaterialization, error) {
	return model.NativeMaterialization{}, model.ErrNativeEpicNotFound
}

type fakePlanningLauncher struct {
	result    PlanningSession
	err       error
	calls     []PlanningSessionRequest
	stops     []PlanningSession
	probes    []PlanningSession
	dead      map[string]bool
	onLaunch  func(PlanningSessionRequest)
	prompts   []PlanningSessionRequest
	promptErr error
}

func (f *fakePlanningLauncher) PromptPlanningSession(_ context.Context, _ PlanningSession, req PlanningSessionRequest) error {
	f.prompts = append(f.prompts, req)
	return f.promptErr
}

func (f *fakePlanningLauncher) LaunchPlanningSession(_ context.Context, req PlanningSessionRequest) (PlanningSession, error) {
	f.calls = append(f.calls, req)
	if f.onLaunch != nil {
		f.onLaunch(req)
	}
	return f.result, f.err
}
func (f *fakePlanningLauncher) ProbePlanningSession(_ context.Context, session PlanningSession) (bool, error) {
	f.probes = append(f.probes, session)
	return !f.dead[session.ID], f.err
}
func (f *fakePlanningLauncher) StopPlanningSession(_ context.Context, session PlanningSession) error {
	f.stops = append(f.stops, session)
	return nil
}

func createPouredWorkEpic(t *testing.T, svc *NativeService, goal string) WorkEpic {
	t.Helper()
	epic, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: goal, InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(context.Background(), epic.ID); err != nil {
		t.Fatal(err)
	}
	return epic
}

func pouredIssueID(t *testing.T, svc *NativeService, epicID, kind string) string {
	t.Helper()
	issues, err := svc.ListIssues(context.Background(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Kind == kind {
			return issue.ID
		}
	}
	t.Fatalf("missing %s issue", kind)
	return ""
}

func TestNativePlanClaimPersistsAttemptBeforeLaunching(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	launcher.onLaunch = func(PlanningSessionRequest) {
		attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
		if err != nil || len(attempts) != 1 || attempts[0].Phase != "prepared" {
			t.Fatalf("attempt before launch = %#v, %v", attempts, err)
		}
	}
	claimed, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Attempt.Phase != "active" || claimed.Session.ID != "plan-1" || len(launcher.calls) != 1 || launcher.calls[0].Repository != "/repo" || launcher.calls[0].Title != "plan "+pouredIssueID(t, svc, epic.ID, "plan")+" (@factory)" {
		t.Fatalf("claim = %#v, launches = %#v", claimed, launcher.calls)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Session != claimed.Session {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	if _, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err == nil || len(launcher.calls) != 1 {
		t.Fatalf("second claim = %v, launches = %#v", err, launcher.calls)
	}
}

func TestNativePlanClaimFailureDoesNotDuplicateAttempt(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{err: errors.New("unavailable")}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	if _, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("ClaimPlan error = %v", err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != "terminal" || attempts[0].Failure.Type != "launch_failed" {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	if _, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err == nil || len(launcher.calls) != 1 {
		t.Fatalf("retry error = %v, launches = %#v", err, launcher.calls)
	}
}

func TestNativePlanPromptFailureReopensPlan(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{
		result:    PlanningSession{Platform: "agent", ID: "plan-1"},
		promptErr: errors.New("unavailable"),
	}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	planID := pouredIssueID(t, svc, epic.ID, "plan")
	if _, err := svc.ClaimPlan(context.Background(), epic.ID, planID); !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("ClaimPlan error = %v", err)
	}
	issues, err := svc.ListIssues(context.Background(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == planID && issue.Status != "open" {
			t.Fatalf("plan status = %q, want open", issue.Status)
		}
	}
}

func TestNativePlanClaimRaceLaunchesOneSession(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"))
			errs <- err
		}()
	}
	close(start)
	var successes int
	for range 2 {
		if <-errs == nil {
			successes++
		}
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || successes != 1 || len(launcher.calls) != 1 || len(attempts) != 1 {
		t.Fatalf("successes = %d, launches = %#v, attempts = %#v, error = %v", successes, launcher.calls, attempts, err)
	}
}

func TestNativePlanClaimDisposesSessionWhenAttemptCannotBeActivated(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(failingActivationStore{db}, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	if _, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err == nil {
		t.Fatal("ClaimPlan unexpectedly succeeded")
	}
	if len(launcher.stops) != 1 || launcher.stops[0].ID != "plan-1" {
		t.Fatalf("disposed sessions = %#v", launcher.stops)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != "terminal" || attempts[0].Failure.Type != "activation_failed" {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
}

func TestNativeStartPreservesLivePlanningSessionAndTerminatesUnstartedClaim(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	liveEpic := createPouredWorkEpic(t, svc, "Live")
	if _, err := svc.ClaimPlan(context.Background(), liveEpic.ID, pouredIssueID(t, svc, liveEpic.ID, "plan")); err != nil {
		t.Fatal(err)
	}
	pendingEpic := createPouredWorkEpic(t, svc, "Pending")
	if _, _, err := db.ClaimFactoryPlan(context.Background(), pendingEpic.ID, pouredIssueID(t, svc, pendingEpic.ID, "plan"), planningProfile, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), pendingEpic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != "terminal" || attempts[0].Failure.Type != "interrupted_startup" {
		t.Fatalf("pending attempts = %#v, %v", attempts, err)
	}
	if len(launcher.calls) != 1 || len(launcher.probes) != 1 || launcher.probes[0].ID != "plan-1" {
		t.Fatalf("launches = %#v, probes = %#v", launcher.calls, launcher.probes)
	}
}

func TestNativeStartTerminatesUnavailablePlanningSession(t *testing.T) {
	for _, tt := range []struct {
		name  string
		dead  bool
		err   error
		phase string
	}{
		{name: "dead session", dead: true, phase: "terminal"},
		{name: "unavailable platform", err: errors.New("platform unavailable"), phase: "active"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}, dead: map[string]bool{"plan-1": tt.dead}}
			svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
			epic := createPouredWorkEpic(t, svc, "Ship")
			if _, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err != nil {
				t.Fatal(err)
			}
			launcher.err = tt.err

			if err := svc.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
			if err != nil || len(attempts) != 1 || string(attempts[0].Phase) != tt.phase || (tt.phase == "terminal" && attempts[0].Failure.Type != "interrupted_startup") || len(launcher.calls) != 1 {
				t.Fatalf("attempts = %#v, launches = %#v, error = %v", attempts, launcher.calls, err)
			}
		})
	}
}

func TestNativeStartAndClaimRaceDoesNotLaunchPreparedPlan(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	if _, _, err := db.ClaimFactoryPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"), planningProfile, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; errs <- svc.Start(context.Background()) }()
	go func() {
		<-start
		_, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"))
		errs <- err
	}()
	close(start)
	<-errs
	<-errs
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != "terminal" || len(launcher.calls) != 0 {
		t.Fatalf("attempts = %#v, launches = %#v, error = %v", attempts, launcher.calls, err)
	}
}

func TestNativeProposalIsImmutableAndScoped(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Ship")
	manifest := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}
	first, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest, RationaleMarkdown: "# Why"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.ContentHash == "" {
		t.Fatalf("proposal = %#v", first)
	}
	got, err := svc.GetProposal(context.Background(), epic.ID, 1)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("GetProposal = %#v, %v", got, err)
	}
	second, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest, RationaleMarkdown: "# Updated"})
	if err != nil || second.Revision != 2 || second.ContentHash == first.ContentHash {
		t.Fatalf("second proposal = %#v, %v", second, err)
	}
	got, err = svc.GetProposal(context.Background(), epic.ID, 1)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("first proposal changed = %#v, %v", got, err)
	}
	history, err := svc.ListProposals(context.Background(), epic.ID)
	if err != nil || !reflect.DeepEqual(history, []ProposalRevision{first, second}) {
		t.Fatalf("proposal history = %#v, %v", history, err)
	}
	if _, err := svc.ListProposals(context.Background(), "missing"); !errors.Is(err, ErrWorkEpicNotFound) {
		t.Fatalf("missing proposal history error = %v", err)
	}
	epic, err = svc.GetWorkEpic(context.Background(), epic.ID)
	if err != nil || epic.Proposal == nil || epic.Proposal.Revision != 2 {
		t.Fatalf("Epic proposal = %#v, %v", epic.Proposal, err)
	}
	if _, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/wrong", Nodes: manifest.Nodes}}); err == nil {
		t.Fatal("out-of-scope proposal was accepted")
	}
	if _, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "one", Type: "implementation", Requirement: "optional"}}}}); err == nil {
		t.Fatal("proposal without required implementation was accepted")
	}
}

// A Planning Session proves it owns an Epic with the attempt token minted at
// claim time; a token from another Epic (or a forged one) cannot reset that
// Epic's approval by submitting a proposal for it.
func TestNativeProposalWithAttemptTokenIsBoundToItsEpic(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}})
	mine, other := createPouredWorkEpic(t, svc, "Mine"), createPouredWorkEpic(t, svc, "Other")
	claimed, err := svc.ClaimPlan(context.Background(), mine.ID, pouredIssueID(t, svc, mine.ID, "plan"))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Attempt.AgentToken == "" {
		t.Fatalf("plan claim minted no attempt token: %#v", claimed.Attempt)
	}
	manifestFor := func(epic WorkEpic) ProposalManifest {
		return ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}
	}
	if _, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: mine.ID, Manifest: manifestFor(mine), AttemptID: claimed.Attempt.ID, AttemptToken: claimed.Attempt.AgentToken}); err != nil {
		t.Fatalf("own-epic proposal rejected: %v", err)
	}
	for name, req := range map[string]SubmitProposalRequest{
		"other epic":   {EpicID: other.ID, Manifest: manifestFor(other), AttemptID: claimed.Attempt.ID, AttemptToken: claimed.Attempt.AgentToken},
		"forged token": {EpicID: mine.ID, Manifest: manifestFor(mine), AttemptID: claimed.Attempt.ID, AttemptToken: "fat_forged"},
		"token only":   {EpicID: mine.ID, Manifest: manifestFor(mine), AttemptToken: claimed.Attempt.AgentToken},
	} {
		if _, err := svc.SubmitProposal(context.Background(), req); err == nil {
			t.Fatalf("%s: proposal was accepted", name)
		}
	}
	proposals, err := svc.ListProposals(context.Background(), other.ID)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("other epic gained proposals: %#v, %v", proposals, err)
	}
}

func TestNativeProposalAttemptTokenExpiresAfterTerminalDecision(t *testing.T) {
	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}})
			epic := createPouredWorkEpic(t, svc, "Terminal")
			claimed, err := svc.ClaimPlan(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"))
			if err != nil {
				t.Fatal(err)
			}
			manifest := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}
			request := SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest, AttemptID: claimed.Attempt.ID, AttemptToken: claimed.Attempt.AgentToken}
			proposal, err := svc.SubmitProposal(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.DecidePlanGate(t.Context(), epic.ID, action, PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.SubmitProposal(t.Context(), request); !errors.Is(err, ErrActionNotPermitted) {
				t.Fatalf("replay error = %v, want ErrActionNotPermitted", err)
			}
		})
	}
}

func TestNativeProposalReclassificationNeedsApprovalAndPreservesHistory(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Ship")
	base := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "first", Type: "implementation", Requirement: "required"}}}
	if _, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "bad", Type: "implementation", Requirement: "required", Pinned: true}}}}); err == nil {
		t.Fatal("accepted a pinned executable node")
	}
	first, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: base})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash}); err != nil {
		t.Fatal(err)
	}
	firstMaterialization, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"))
	if err != nil {
		t.Fatal(err)
	}
	revised := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "first", Type: "implementation", Requirement: "reference", Pinned: true}, {Key: "second", Type: "implementation", Requirement: "required"}}}
	second, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: revised})
	if err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(context.Background(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, firstMaterialization.ImplementationID) {
		t.Fatal("unapproved reclassification removed active work")
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: second.Revision, ExpectedHash: second.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	issues, err = svc.ListIssues(context.Background(), epic.ID)
	if err != nil || hasIssue(issues, firstMaterialization.ImplementationID) || !hasManifestKey(issues, "second") {
		t.Fatalf("active issues = %#v, %v", issues, err)
	}
	history, err := svc.ListProposals(context.Background(), epic.ID)
	if err != nil || len(history) != 2 || history[0].Manifest.Nodes[0].Requirement != "required" || history[1].Manifest.Nodes[0].Requirement != "reference" {
		t.Fatalf("proposal history = %#v, %v", history, err)
	}
}

func hasIssue(issues []Issue, id string) bool {
	for _, issue := range issues {
		if issue.ID == id {
			return true
		}
	}
	return false
}

func hasManifestKey(issues []Issue, key string) bool {
	for _, issue := range issues {
		if issue.ManifestKey == key {
			return true
		}
	}
	return false
}

func TestNativePlanGateRequiresExactNewProposalAfterRevisionRequest(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Ship")
	manifest := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}
	first, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if gate, err := svc.DecidePlanGate(context.Background(), epic.ID, "revise", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash, Feedback: "split the work"}); err != nil || gate.Resolution != "revision_requested" || gate.Feedback != "split the work" {
		t.Fatalf("revise = %#v, %v", gate, err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash}); err == nil {
		t.Fatal("approved revision-requested proposal")
	}
	second, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest, RationaleMarkdown: "revised"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash}); err == nil {
		t.Fatal("approved stale proposal")
	}
	gate, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: second.Revision, ExpectedHash: second.ContentHash})
	if err != nil || gate.Resolution != "approved" || gate.Outcome != "succeeded" {
		t.Fatalf("approve = %#v, %v", gate, err)
	}
	if _, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest, RationaleMarkdown: "changed"}); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetWorkEpic(context.Background(), epic.ID)
	if err != nil || detail.PlanGate == nil || detail.PlanGate.Resolution != "open" {
		t.Fatalf("invalidated gate = %#v, %v", detail.PlanGate, err)
	}
}

func TestNativePlanGateRejectCancelsOnlyUnstartedWork(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Ship")
	manifest := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"), planningProfile, time.Now()); err != nil {
		t.Fatal(err)
	}
	gate, err := svc.DecidePlanGate(context.Background(), epic.ID, "reject", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash})
	if err != nil || gate.Resolution != "rejected" || gate.Outcome != "failed" || !reflect.DeepEqual(gate.ReviewIssueIDs, []string{pouredIssueID(t, svc, epic.ID, "plan")}) {
		t.Fatalf("reject = %#v, %v", gate, err)
	}
	issues, err := svc.ListIssues(context.Background(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == pouredIssueID(t, svc, epic.ID, "plan") && issue.Status != "in_progress" {
			t.Fatalf("started issue changed: %#v", issue)
		}
	}
}

func TestNativeMaterializationCreatesApprovedImplementationAtomically(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"))
	if err != nil || first.ImplementationID == "" || first.ManifestKey != "implement" || len(launcher.calls) != 0 {
		t.Fatalf("Materialize = %#v, %v; launches = %#v", first, err, launcher.calls)
	}
	second, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"))
	if err != nil || second != first {
		t.Fatalf("repeated Materialize = %#v, %v; want %#v", second, err, first)
	}
	issues, err := svc.ListIssues(context.Background(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var implementation, materialization Issue
	for _, issue := range issues {
		if issue.ID == first.ImplementationID {
			implementation = issue
		}
		if issue.ID == first.IssueID {
			materialization = issue
		}
	}
	if implementation.Kind != "implementation" || implementation.ParentID != pouredIssueID(t, svc, epic.ID, "mol") || implementation.Requirement != "required" || materialization.Status != "closed" {
		t.Fatalf("issues = %#v", issues)
	}
	if len(issues) != 5 {
		t.Fatalf("materialization duplicated implementation: %#v", issues)
	}
}

func TestNativeMaterializeRequiresPlanningStoreAndApproval(t *testing.T) {
	if _, err := NewNative(&nativeStoreFake{}).Materialize(context.Background(), "missing", "issue"); !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("Materialize without planning store = %v", err)
	}
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}).Materialize(context.Background(), "missing", "issue"); err == nil {
		t.Fatal("Materialize missing approval unexpectedly succeeded")
	}
	if _, err := NewNativeWithPlanning(materializationNotFoundStore{db}, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}).Materialize(context.Background(), "missing-epic", "issue"); !errors.Is(err, ErrWorkEpicNotFound) {
		t.Fatalf("Materialize missing Epic error = %v", err)
	}
}

func TestNativePlanGateRejectsInvalidAndUnavailableProposals(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	if _, err := svc.DecidePlanGate(context.Background(), "epic", "approve", PlanGateDecisionRequest{}); err == nil {
		t.Fatal("DecidePlanGate accepted missing proposal identity")
	}
	if _, err := svc.DecidePlanGate(context.Background(), "missing", "approve", PlanGateDecisionRequest{ExpectedRevision: 1, ExpectedHash: "hash"}); err == nil {
		t.Fatal("DecidePlanGate accepted an unavailable gate")
	}
}

func TestNativeServiceIssueControlsAndQueueState(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	if status := svc.Status(context.Background()); status.Health != HealthHealthy || !status.Idle || !status.DispatchOwner {
		t.Fatalf("Status = %#v", status)
	}
	if policy, err := svc.GetCapacityPolicy(context.Background()); err != nil || policy.GlobalCapacity == 0 {
		t.Fatalf("GetCapacityPolicy = %#v, %v", policy, err)
	}
	epic := createPouredWorkEpic(t, svc, "Ship")
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	work, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeferIssue(context.Background(), epic.ID, work.ImplementationID, "waiting"); err != nil {
		t.Fatal(err)
	}
	if queue, err := svc.Queue(context.Background()); err != nil || len(queue) != 1 || queue[0].State != DispatchDeferred || queue[0].OutcomeReason != "waiting" {
		t.Fatalf("deferred Queue = %#v, %v", queue, err)
	}
	if err := svc.ResumeIssue(context.Background(), epic.ID, work.ImplementationID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RetryIssueAt(context.Background(), epic.ID, work.ImplementationID, time.Now()); err == nil {
		t.Fatal("RetryIssueAt accepted a past time")
	}
	wakeAt := time.Now().Add(time.Hour)
	if err := svc.RetryIssueAt(context.Background(), epic.ID, work.ImplementationID, wakeAt); err != nil {
		t.Fatal(err)
	}
	if queue, err := svc.Queue(context.Background()); err != nil || len(queue) != 1 || queue[0].State != DispatchRetryWait || queue[0].RetryAt != wakeAt.UnixMilli() {
		t.Fatalf("retry Queue = %#v, %v", queue, err)
	}
}

func TestNativeEpicDetailIncludesPlanningAttempt(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	if _, err := svc.ClaimPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetWorkEpic(context.Background(), epic.ID)
	if err != nil || len(detail.Attempts) != 1 || detail.Attempts[0].Session.ID != "plan-1" {
		t.Fatalf("detail = %#v, %v", detail, err)
	}
}
