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

type fakeImplementationLauncher struct {
	calls      []ImplementationSessionRequest
	err        error
	probeErr   error
	result     PlanningSession
	stops      []PlanningSession
	replies    []string
	replyErr   error
	answered   bool
	prompts    []ImplementationSessionRequest
	promptErr  error
	onPrompt   func(ImplementationSessionRequest)
	launched   chan struct{}
	dead       bool
	handoffErr error
	handoffs   int
	store      *state.DB
}

func (f *fakeImplementationLauncher) ValidateImplementationHandoff(context.Context, string, string, string, model.FactoryAttemptPolicy) error {
	f.handoffs++
	return f.handoffErr
}

type failingDispatchStore struct {
	*state.DB
	listErr error
}

func (s *failingDispatchStore) ListFactoryEpics(ctx context.Context) ([]model.NativeEpic, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.DB.ListFactoryEpics(ctx)
}

func (f *fakeImplementationLauncher) PromptImplementationSession(_ context.Context, _ PlanningSession, req ImplementationSessionRequest) error {
	f.prompts = append(f.prompts, req)
	if f.onPrompt != nil {
		f.onPrompt(req)
	}
	return f.promptErr
}

func (f *fakeImplementationLauncher) LaunchImplementationSession(_ context.Context, req ImplementationSessionRequest) (PlanningSession, error) {
	if f.store != nil {
		if err := f.store.SetFactoryAttemptDeliveryTarget(context.Background(), req.AttemptID, "github", "github.com", "acme/repo", time.Now()); err != nil {
			return PlanningSession{}, err
		}
	}
	f.calls = append(f.calls, req)
	if f.launched != nil {
		f.launched <- struct{}{}
	}
	if f.err != nil {
		return f.result, f.err
	}
	if f.result.ID != "" || f.result.Platform != "" {
		return f.result, nil
	}
	return PlanningSession{Platform: "opencode", ID: "implementation-1"}, nil
}
func (f *fakeImplementationLauncher) ProbeImplementationSession(context.Context, PlanningSession) (bool, error) {
	return !f.dead, f.probeErr
}
func (f *fakeImplementationLauncher) StopImplementationSession(_ context.Context, session PlanningSession) error {
	f.stops = append(f.stops, session)
	return nil
}
func (f *fakeImplementationLauncher) RespondImplementationPermission(_ context.Context, session PlanningSession, requestID, reply string) error {
	f.replies = append(f.replies, session.ID+":"+requestID+":"+reply)
	if f.replyErr == nil {
		f.answered = true
	}
	return f.replyErr
}
func (f *fakeImplementationLauncher) ImplementationPermissionPending(context.Context, PlanningSession, string) (bool, error) {
	return !f.answered, nil
}

type flakyAuthorityStore struct {
	*state.DB
	failCompletion bool
}

func (s *flakyAuthorityStore) CompleteFactoryAuthorityEscalationGate(ctx context.Context, gateID, action string, at time.Time) (model.AuthorityEscalationGate, error) {
	if s.failCompletion {
		s.failCompletion = false
		return model.AuthorityEscalationGate{}, errors.New("completion failed")
	}
	return s.DB.CompleteFactoryAuthorityEscalationGate(ctx, gateID, action, at)
}

func TestNativeAuthorityEscalationIsOneTimeAndDoesNotWidenProfile(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	store := &flakyAuthorityStore{DB: db}
	svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "authority")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Materialize(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	if err = svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, handled, err := svc.EscalatePermission(t.Context(), "implementation-1", "request-1", "bash", "git status"); err != nil || handled {
		t.Fatalf("in-profile escalation = handled %v, err %v", handled, err)
	}
	gate, handled, err := svc.EscalatePermission(t.Context(), "implementation-1", "request-1", "external_directory", "/outside")
	if err != nil || !handled || gate.Resolution != "open" {
		t.Fatalf("EscalatePermission = %#v, %v, %v", gate, handled, err)
	}
	duplicate, handled, err := svc.EscalatePermission(t.Context(), "implementation-1", "request-1", "external_directory", "/outside")
	if err != nil || !handled || duplicate.IssueID != gate.IssueID {
		t.Fatalf("duplicate = %#v, %v, %v", duplicate, handled, err)
	}
	attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err == nil {
		t.Fatal("completed attempt with open authority gate")
	}
	launcher.replyErr = errors.New("delivery failed")
	if _, err = svc.ResolveAuthorityEscalationGate(t.Context(), gate.IssueID, "approve"); err == nil {
		t.Fatal("failed permission delivery returned no error")
	}
	pending, found, err := db.GetFactoryAuthorityEscalationGate(t.Context(), gate.IssueID)
	if err != nil || !found || pending.Resolution != "approve_pending" {
		t.Fatalf("durably resolved gate = %#v, %v, %v", pending, found, err)
	}
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err == nil {
		t.Fatal("completed attempt with undelivered authority decision")
	}
	if err := db.SetFactoryCapacityPolicy(t.Context(), model.FactoryCapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "create", EpicID: epic.ID, ParentID: pouredIssueID(t, svc, epic.ID, "mol"), Kind: "task", Title: "Other work"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil || len(launcher.calls) != 1 {
		t.Fatalf("pending gate released the shared Epic workspace: calls=%d, err=%v", len(launcher.calls), err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil || !hasAuthorityGate(issues, gate.IssueID, "approve_pending") {
		t.Fatalf("pending gate after refresh = %#v, %v", issues, err)
	}
	launcher.replyErr = nil
	store.failCompletion = true
	if _, err := svc.ResolveAuthorityEscalationGate(t.Context(), gate.IssueID, "approve"); err == nil {
		t.Fatal("failed gate completion returned no error")
	}
	resolved, err := svc.ResolveAuthorityEscalationGate(t.Context(), gate.IssueID, "approve")
	if err != nil || resolved.Resolution != "approve" {
		t.Fatalf("reconciling delivered permission: %v", err)
	}
	if len(launcher.replies) != 2 || launcher.replies[1] != "implementation-1:request-1:once" {
		t.Fatalf("replies = %v", launcher.replies)
	}
	if _, err = svc.ResolveAuthorityEscalationGate(t.Context(), gate.IssueID, "reject"); err == nil {
		t.Fatal("resolved approval changed to rejection")
	}
	attempts, err = db.ListFactoryAttempts(t.Context(), epic.ID)
	if err != nil || attempts[0].FrozenPolicy.Profile != "factory-implement/v1" {
		t.Fatalf("attempt profile = %#v, %v", attempts, err)
	}
}

func TestNativeIdentifiesImplementationSessions(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "identify implementation")
	if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "create", EpicID: epic.ID, ParentID: pouredIssueID(t, svc, epic.ID, "mol"), Kind: "task", Title: "Implement"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}

	for session, want := range map[string]bool{"implementation-1": true, "other": false} {
		got, err := svc.IsImplementationSession(t.Context(), session)
		if err != nil || got != want {
			t.Fatalf("IsImplementationSession(%q) = %v, %v, want %v", session, got, err, want)
		}
	}
}

func hasAuthorityGate(issues []Issue, id, resolution string) bool {
	for _, issue := range issues {
		if issue.ID == id && issue.Authority != nil && issue.Authority.Resolution == resolution {
			return true
		}
	}
	return false
}

func TestNativeDispatchRunsReadyTask(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "create", EpicID: epic.ID, ParentID: pouredIssueID(t, svc, epic.ID, "mol"), Kind: "task", Title: "Implement it"}); err != nil {
		t.Fatal(err)
	}
	taskID := pouredIssueID(t, svc, epic.ID, "task")
	queue, err := svc.Queue(t.Context())
	if err != nil || len(queue) != 1 || queue[0].ID != taskID || queue[0].State != DispatchReady {
		t.Fatalf("queue = %#v, %v", queue, err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].WorkID != taskID || launcher.calls[0].Branch != "factory/"+epic.ID {
		t.Fatalf("launches = %#v", launcher.calls)
	}
	attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", ""); err == nil {
		t.Fatal("completed without pull request URL")
	}
	launcher.handoffErr = errors.New("Factory worktree has uncommitted changes")
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err == nil {
		t.Fatal("completed a dirty handoff")
	}
	launcher.handoffErr = nil
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err != nil {
		t.Fatal(err)
	}
	if launcher.handoffs != 3 || len(launcher.stops) != 1 {
		t.Fatalf("handoffs/stops = %d/%d", launcher.handoffs, len(launcher.stops))
	}
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err != nil {
		t.Fatalf("idempotent completion: %v", err)
	}
	archived, err := db.ArchivedSessions(t.Context())
	_, found := archived[state.Key{Platform: "opencode", SessionID: "implementation-1"}]
	if err != nil || !found {
		t.Fatalf("archived sessions = %#v, %v", archived, err)
	}
}

func TestNativeImplementationDispatchClaimsBeforeWorktreeLaunchAndHonorsCapacity(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetFactoryCapacityPolicy(context.Background(), model.FactoryCapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1}); err != nil {
		t.Fatal(err)
	}
	launcher := &fakeImplementationLauncher{}
	promptedActive := false
	launcher.onPrompt = func(req ImplementationSessionRequest) {
		attempt, found, err := db.GetFactoryAttempt(context.Background(), req.AttemptID)
		promptedActive = err == nil && found && attempt.Phase == model.FactoryAttemptActive
	}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	for _, goal := range []string{"First", "Second"} {
		epic := createPouredWorkEpic(t, svc, goal)
		proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.MaterializeFactoryPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"), "factory-materialize/v1", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), "")
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptActive || attempts[0].Session.ID != "implementation-1" {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].Profile != "factory-implement/v1" || launcher.calls[0].Branch == "" {
		t.Fatalf("launches = %#v", launcher.calls)
	}
	queue, err := svc.Queue(context.Background())
	if err != nil || len(queue) != 2 || queue[0].AttemptID == "" || queue[1].State != DispatchReady {
		t.Fatalf("queue = %#v, %v", queue, err)
	}
	if err := db.DeferFactoryIssue(context.Background(), queue[1].EpicID, queue[1].ID, "waiting for review"); err != nil {
		t.Fatal(err)
	}
	queue, err = svc.Queue(context.Background())
	if err != nil || queue[1].State != DispatchDeferred || queue[1].OutcomeReason != "waiting for review" {
		t.Fatalf("deferred queue = %#v, %v", queue, err)
	}
	if err := db.ResumeFactoryIssue(context.Background(), queue[1].EpicID, queue[1].ID); err != nil {
		t.Fatal(err)
	}
	wakeAt := time.Now().Add(time.Hour)
	if err := db.RetryFactoryIssueAt(context.Background(), queue[1].EpicID, queue[1].ID, wakeAt); err != nil {
		t.Fatal(err)
	}
	queue, err = svc.Queue(context.Background())
	if err != nil || queue[1].State != DispatchRetryWait || queue[1].RetryAttempts != 1 || queue[1].RetryAt != wakeAt.UnixMilli() {
		t.Fatalf("retry queue = %#v, %v", queue, err)
	}
	if err := svc.CompleteAttempt(context.Background(), attempts[0].ID, launcher.calls[0].AgentToken, "Implemented and tested.", "https://forge.example/pr/1"); err != nil {
		t.Fatal(err)
	}
	completed, found, err := db.GetFactoryAttempt(context.Background(), attempts[0].ID)
	if err != nil || !found || completed.Outcome != model.FactoryAttemptSucceeded || completed.Result == nil || completed.Result.Summary != "Implemented and tested." || completed.Result.PRURL != "https://forge.example/pr/1" {
		t.Fatalf("completed attempt = %#v, %v, %v", completed, found, err)
	}
	issues, err := svc.ListIssues(context.Background(), attempts[0].EpicID)
	if err != nil {
		t.Fatal(err)
	}
	conclusion := ""
	for _, issue := range issues {
		if issue.ID == attempts[0].WorkID {
			conclusion = issue.Conclusion
		}
	}
	if conclusion != "Implemented and tested." {
		t.Fatalf("issue conclusion = %q", conclusion)
	}
	if !promptedActive {
		t.Fatal("implementation was prompted before attempt activation")
	}
}

func TestNativeImplementationCompletionDispatchesNextReadyWork(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetFactoryCapacityPolicy(t.Context(), model.FactoryCapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1}); err != nil {
		t.Fatal(err)
	}
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	for _, goal := range []string{"First", "Second"} {
		epic := createPouredWorkEpic(t, svc, goal)
		proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Materialize(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
			t.Fatal(err)
		}
	}
	attempts, err := db.ListFactoryAttempts(t.Context(), "")
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	launcher.launched = make(chan struct{}, 1)
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if err := svc.CompleteAttempt(t.Context(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-launcher.launched:
	case <-time.After(time.Second):
		t.Fatal("completion did not trigger dispatch")
	}
	if len(launcher.calls) != 2 {
		t.Fatalf("launches = %#v", launcher.calls)
	}
}

func TestNativeImplementationLaunchFailureLeavesTerminalAttempt(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{err: errors.New("unavailable")}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MaterializeFactoryPlan(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"), "factory-materialize/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal || attempts[0].Failure.Type != "launch_failed" {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	issues, err := svc.ListIssues(context.Background(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Kind == "implementation" && issue.Status != "retry_wait" {
			t.Fatalf("failed launch left implementation status %q", issue.Status)
		}
	}
	if err := db.WakeFactoryRetries(context.Background(), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	launcher.err = nil
	t.Cleanup(svc.Close)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err = db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 2 || attempts[1].Phase != model.FactoryAttemptActive {
		t.Fatalf("recovered attempts = %#v, %v", attempts, err)
	}
}

func TestNativeImplementationDispatchRejectsEmptySession(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{result: PlanningSession{Platform: "opencode"}}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal || attempts[0].Failure.Type != "launch_failed" {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
}

func TestNativeStartKeepsImplementationAttemptWhenProbeFails(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	launcher.probeErr = errors.New("temporary probe failure")
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptActive {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
}

func TestNativeImplementationDispatchStopsPartialLaunchAndRecoversDeadSession(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{result: PlanningSession{Platform: "opencode", ID: "partial"}, err: errors.New("unavailable")}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stops) != 1 || launcher.stops[0].ID != "partial" {
		t.Fatalf("stopped sessions = %#v", launcher.stops)
	}
	queue, err := svc.Queue(context.Background())
	if err != nil || len(queue) != 1 || queue[0].State != DispatchRetryWait || queue[0].RetryAttempts != 1 || queue[0].RetryAt == 0 {
		t.Fatalf("queue after failed launch = %#v, %v", queue, err)
	}

	launcher.err = nil
	launcher.result = PlanningSession{Platform: "opencode", ID: "implementation-1"}
	recoveryEpic := createPouredWorkEpic(t, svc, "Recover")
	recoveryProposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: recoveryEpic.ID, Manifest: ProposalManifest{EpicID: recoveryEpic.ID, MolID: pouredIssueID(t, svc, recoveryEpic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), recoveryEpic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: recoveryProposal.Revision, ExpectedHash: recoveryProposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(context.Background(), recoveryEpic.ID, pouredIssueID(t, svc, recoveryEpic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	launcher.dead = true
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), recoveryEpic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal || attempts[0].Failure.Type != "interrupted_runtime" {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	issues, err := svc.ListIssues(context.Background(), recoveryEpic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Kind == "implementation" && issue.Status != "retry_wait" {
			t.Fatalf("implementation status = %q, want retry_wait", issue.Status)
		}
	}
}

func TestNativeRecoveryGateReleasesCapacityAndSurvivesRestart(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetFactoryCapacityPolicy(context.Background(), model.FactoryCapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1}); err != nil {
		t.Fatal(err)
	}
	launcher := &fakeImplementationLauncher{}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	first := createPouredWorkEpic(t, svc, "First")
	second := createPouredWorkEpic(t, svc, "Second")
	for _, epic := range []WorkEpic{first, second} {
		proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), first.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("first attempts = %#v, %v", attempts, err)
	}
	gate, err := svc.CreateRecoveryGate(context.Background(), attempts[0].ID, launcher.calls[0].AgentToken, "Which API?", "This affects existing data.", []string{"A", "B"})
	if err != nil || gate.Resolution != "open" {
		t.Fatalf("CreateRecoveryGate = %#v, %v", gate, err)
	}
	if err := svc.CompleteAttempt(context.Background(), attempts[0].ID, launcher.calls[0].AgentToken, "done", "https://forge.example/pr/1"); err == nil {
		t.Fatal("completed attempt with open recovery gate")
	}
	issues, err := svc.ListIssues(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == gate.IssueID && (issue.Recovery == nil || issue.Recovery.AttemptID != attempts[0].ID || issue.Recovery.Reason != "This affects existing data.") {
			t.Fatalf("listed recovery issue = %#v", issue)
		}
	}
	launcher.dead = true
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err = db.ListFactoryAttempts(context.Background(), first.ID)
	if err != nil || attempts[0].Phase != model.FactoryAttemptActive {
		t.Fatalf("paused attempt after restart = %#v, %v", attempts, err)
	}
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 2 {
		t.Fatalf("launches after recovery = %#v", launcher.calls)
	}
	if _, err := svc.ResolveRecoveryGate(context.Background(), gate.IssueID, "resume", "Use A"); err == nil {
		t.Fatal("resumed despite occupied capacity")
	}
}

func TestNativeRecoveryGateRetryAndCancel(t *testing.T) {
	for _, action := range []string{"retry", "cancel"} {
		t.Run(action, func(t *testing.T) {
			db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			launcher := &fakeImplementationLauncher{}
			svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
			epic := createPouredWorkEpic(t, svc, action)
			proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
				t.Fatal(err)
			}
			if err := svc.Dispatch(context.Background()); err != nil {
				t.Fatal(err)
			}
			attempts, _ := db.ListFactoryAttempts(context.Background(), epic.ID)
			gate, err := svc.CreateRecoveryGate(context.Background(), attempts[0].ID, launcher.calls[0].AgentToken, "Question", "Reason", nil)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := svc.ResolveRecoveryGate(context.Background(), gate.IssueID, action, "Answer")
			if err != nil || resolved.Resolution != action {
				t.Fatalf("ResolveRecoveryGate = %#v, %v", resolved, err)
			}
			attempts, _ = db.ListFactoryAttempts(context.Background(), epic.ID)
			if action == "retry" && (len(attempts) != 2 || attempts[0].Outcome != model.FactoryAttemptCancelled || attempts[1].Phase != model.FactoryAttemptActive || len(launcher.stops) != 1) {
				t.Fatalf("retry attempts = %#v", attempts)
			}
			if action == "retry" {
				queue, err := svc.Queue(context.Background())
				if err != nil || len(queue) != 1 || queue[0].State != DispatchRunning || queue[0].AttemptID != attempts[1].ID {
					t.Fatalf("retry queue = %#v, %v", queue, err)
				}
			}
			if action == "cancel" && (len(attempts) != 1 || attempts[0].Outcome != model.FactoryAttemptCancelled || len(launcher.stops) != 1) {
				t.Fatalf("cancel attempts/stops = %#v %#v", attempts, launcher.stops)
			}
		})
	}
}

// A hand-built graph whose only remaining work exhausted its launch retries
// must be reported stuck and become dispatchable again after ReopenIssue.
func TestNativeStuckEpicRecoversThroughReopenIssue(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{err: errors.New("unavailable")}
	store := &failingDispatchStore{DB: db}
	svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Ship")
	molID := pouredIssueID(t, svc, epic.ID, "mol")
	if err := svc.MutateGraph(context.Background(), GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "task", Title: "Only task"}); err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: molID, Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	taskID := pouredIssueID(t, svc, epic.ID, "task")
	for i := 0; i < 4; i++ {
		if err := db.WakeFactoryRetries(context.Background(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := svc.Dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.GetWorkEpic(context.Background(), epic.ID)
	if err != nil || !got.Progress.Stuck || got.Progress.RequiredTotal != 4 || got.Progress.RequiredSucceeded != 3 || !reflect.DeepEqual(got.Progress.ClosureBlockers, []string{"Only task"}) {
		t.Fatalf("stuck epic = %#v, %v", got.Progress, err)
	}
	if err := svc.ReopenIssue(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err == nil {
		t.Fatal("reopened a plan")
	}
	launcher.err = nil
	store.listErr = errors.New("dispatch unavailable")
	if err := svc.ReopenIssue(context.Background(), epic.ID, taskID); err != nil {
		t.Fatalf("reopen reported a dispatch failure after persisting: %v", err)
	}
	store.listErr = nil
	if err := svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 5 {
		t.Fatalf("reopen did not dispatch: %d launches", len(launcher.calls))
	}
	got, err = svc.GetWorkEpic(context.Background(), epic.ID)
	if err != nil || got.Progress.Stuck {
		t.Fatalf("running epic reported stuck: %#v, %v", got.Progress, err)
	}
	if NewNative(&nativeStoreFake{}).ReopenIssue(context.Background(), epic.ID, taskID) == nil {
		t.Fatal("reopen without store succeeded")
	}
}
