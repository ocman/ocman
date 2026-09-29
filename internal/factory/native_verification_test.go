package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type verificationStoreFake struct {
	nativeStore
	issues []model.NativeIssue
	err    error
}

func (f verificationStoreFake) ListFactoryIssues(context.Context, string) ([]model.NativeIssue, error) {
	return f.issues, f.err
}

type checkerFake struct {
	ImplementationLauncher
	mu       sync.Mutex
	passed   bool
	err      error
	runs     int
	messages []string
	notified chan struct{}
}

func (f *checkerFake) RunVerificationChecks(_ context.Context, repo, branch, target string, commands []string) (bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	return f.passed, "ran " + strings.Join(commands, ",") + " in " + repo + "@" + branch + " vs " + target, f.err
}

func (f *checkerFake) NotifyImplementationSession(_ context.Context, _ PlanningSession, message string) error {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
	f.notified <- struct{}{}
	return nil
}

func verificationAttempt() model.FactoryAttempt {
	return model.FactoryAttempt{ID: "att-1", EpicID: "e1", WorkID: "verify-1", FrozenPolicy: model.FactoryAttemptPolicy{Repository: "/repo", Branch: "factory/e1", TargetBranch: "main", CheckpointSHA: "abc"}}
}

func verificationService(commands []string, checker *checkerFake) *NativeService {
	step := &model.WorkflowStep{Key: "verify", Kind: "verification", Config: model.WorkflowStepConfig{Commands: commands}}
	store := verificationStoreFake{issues: []model.NativeIssue{
		{ID: "impl-1", Kind: "task", Project: "/repo", Title: "API", Status: "closed", Description: "Build it.\n\nAcceptance criteria:\n- [ ] returns 200"},
		{ID: "ref-1", Kind: "task", Project: "/repo", Requirement: "reference", Title: "Reference"},
		{ID: "other-1", Kind: "task", Project: "/other", Title: "Other project"},
		{ID: "verify-1", Kind: "task", Project: "/repo", Workflow: step},
	}}
	s := &NativeService{store: store}
	if checker != nil {
		s.implementation = checker
	}
	return s
}

func TestGateVerificationRejectsMovedBranch(t *testing.T) {
	s := verificationService(nil, nil)
	if _, err := s.gateVerification(t.Context(), verificationAttempt(), "def"); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "must not change the shared branch") {
		t.Fatalf("moved branch error = %v", err)
	}
	if suffix, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); err != nil || suffix != "" {
		t.Fatalf("unchanged branch without commands = %q, %v", suffix, err)
	}
	implementation := verificationAttempt()
	implementation.WorkID = "impl-1"
	if _, err := s.gateVerification(t.Context(), implementation, "def"); err != nil {
		t.Fatalf("implementation attempts may move the branch: %v", err)
	}
	if _, err := (&NativeService{store: verificationStoreFake{err: errors.New("db down")}}).gateVerification(t.Context(), implementation, "x"); err == nil {
		t.Fatal("store failure was ignored")
	}
}

func TestGateVerificationRunsCommandsAndReportsBackToValidator(t *testing.T) {
	for _, tc := range []struct {
		name    string
		passed  bool
		runErr  error
		verdict string
	}{
		{"pass", true, nil, "PASSED"},
		{"fail", false, nil, "FAILED"},
		{"runner error", true, errors.New("no host"), "FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := &checkerFake{passed: tc.passed, err: tc.runErr, notified: make(chan struct{}, 1)}
			s := verificationService([]string{"make lint", "make test"}, checker)
			if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrVerificationChecksPending) {
				t.Fatalf("first completion = %v, want pending", err)
			}
			select {
			case <-checker.notified:
			case <-time.After(5 * time.Second):
				t.Fatal("validator was never told the results")
			}
			checker.mu.Lock()
			message := checker.messages[0]
			checker.mu.Unlock()
			if !strings.Contains(message, tc.verdict) || !strings.Contains(message, "att-1") {
				t.Fatalf("message = %s", message)
			}
			suffix, err := s.gateVerification(t.Context(), verificationAttempt(), "abc")
			if tc.verdict == "PASSED" {
				if err != nil || !strings.Contains(suffix, "Factory checks passed at abc: make lint; make test") {
					t.Fatalf("passed completion = %q, %v", suffix, err)
				}
			} else if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "request_recovery") {
				t.Fatalf("failed completion = %v", err)
			}
			if checker.runs != 1 {
				t.Fatalf("checks ran %d times, want 1", checker.runs)
			}
			s.forgetVerificationChecks("att-1")
			if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrVerificationChecksPending) {
				t.Fatalf("forgotten checks = %v, want a fresh run", err)
			}
			<-checker.notified
		})
	}
}

func TestGateVerificationPendingWhileRunningAndUnavailableWithoutChecker(t *testing.T) {
	s := verificationService([]string{"true"}, nil)
	if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("no checker = %v", err)
	}
	s.checks = map[string]verificationCheck{"att-1": {head: "abc"}}
	s.implementation = &checkerFake{}
	if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrVerificationChecksPending) {
		t.Fatalf("running checks = %v", err)
	}
}

func TestVerificationCriteriaListsOnlyProjectImplementationWork(t *testing.T) {
	got := verificationService(nil, nil).verificationCriteria(t.Context(), "e1", "/repo")
	if !strings.Contains(got, "impl-1 — API (closed)") || !strings.Contains(got, "returns 200") {
		t.Fatalf("criteria = %s", got)
	}
	for _, excluded := range []string{"ref-1", "other-1", "verify-1"} {
		if strings.Contains(got, excluded) {
			t.Errorf("criteria include %s:\n%s", excluded, got)
		}
	}
	if got := verificationService(nil, nil).verificationCriteria(t.Context(), "e1", "/none"); got != "(none recorded)" {
		t.Fatalf("empty criteria = %q", got)
	}
	failing := &NativeService{store: verificationStoreFake{err: errors.New("db down")}}
	if got := failing.verificationCriteria(t.Context(), "e1", "/repo"); !strings.Contains(got, "db down") {
		t.Fatalf("store failure = %q", got)
	}
}

func TestFoldAcceptanceCriteria(t *testing.T) {
	for _, tc := range []struct {
		name string
		node ManifestNode
		want string
		err  string
	}{
		{"folds a checklist", ManifestNode{Type: "implementation", Requirement: "required", Description: "Build it.", AcceptanceCriteria: []string{"returns  200", "logs\nonce"}}, "Build it.\n\nAcceptance criteria:\n- [ ] returns 200\n- [ ] logs once", ""},
		{"optional work needs criteria too", ManifestNode{Type: "implementation", Requirement: "optional"}, "", "requires 1–20"},
		{"too many", ManifestNode{Type: "implementation", Requirement: "required", AcceptanceCriteria: make([]string, 21)}, "", "requires 1–20"},
		{"blank criterion", ManifestNode{Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{" "}}, "", "1–1000 bytes"},
		{"reference needs none", ManifestNode{Type: "implementation", Requirement: "reference", Description: "Ref"}, "Ref", ""},
		{"delivery rejects criteria", ManifestNode{Type: "delivery", Requirement: "required", AcceptanceCriteria: []string{"x"}}, "", "belong to implementation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := tc.node
			err := foldAcceptanceCriteria(&node)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || node.Description != tc.want || node.AcceptanceCriteria != nil {
				t.Fatalf("node = %+v, %v", node, err)
			}
		})
	}
}

func TestWorkflowVerificationCommands(t *testing.T) {
	source := strings.Replace(tracerWorkflowSource, "  verify:\n    kind: verification\n", "  verify:\n    kind: verification\n    config:\n      commands: [make lint, make test]\n", 1)
	compiled, err := compileNativeFormula(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := compiled.Steps["verify"].Config.Commands; strings.Join(got, "|") != "make lint|make test" {
		t.Fatalf("commands = %v", got)
	}
	builtIn, _ := compileNativeFormula(tracerWorkflowSource)
	if builtIn.Hash == compiled.Hash {
		t.Fatal("commands must be part of the Formula hash")
	}
	for name, bad := range map[string]string{
		"blank command":        strings.Replace(source, "[make lint, make test]", `["  "]`, 1),
		"too many":             strings.Replace(source, "[make lint, make test]", "["+strings.Repeat("x,", 20)+"x]", 1),
		"implementation owner": strings.Replace(tracerWorkflowSource, "      concurrency: 1\n", "      concurrency: 1\n      commands: [make test]\n", 1),
	} {
		if !strings.Contains(bad, "commands:") {
			t.Fatalf("%s: fixture did not apply", name)
		}
		if _, err := compileNativeFormula(bad); err == nil || !strings.Contains(err.Error(), "command") {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

type recordingChecker struct {
	*fakeImplementationLauncher
	passed   bool
	commands []string
	notified chan string
}

func (c *recordingChecker) RunVerificationChecks(_ context.Context, _, _, _ string, commands []string) (bool, string, error) {
	c.commands = commands
	return c.passed, "report", nil
}

func (c *recordingChecker) NotifyImplementationSession(_ context.Context, _ PlanningSession, message string) error {
	c.notified <- message
	return nil
}

func TestWorkflowVerificationCommandsGateCompletion(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &recordingChecker{fakeImplementationLauncher: &fakeImplementationLauncher{store: db}, notified: make(chan string, 1)}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	source := strings.Replace(tracerWorkflowSource, "  verify:\n    kind: verification\n", "  verify:\n    kind: verification\n    config:\n      commands: [make test]\n", 1)
	formula, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/checked", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", AcknowledgeLocalExecution: true, FormulaID: formula.ID, FormulaRevision: formula.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "one", Type: "implementation", Requirement: "required", Title: "API", AcceptanceCriteria: []string{"returns 200"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ImplementationModel: "impl/model", ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	next := func(index int) ImplementationSessionRequest {
		t.Helper()
		launcher.result = PlanningSession{Platform: "opencode", ID: fmt.Sprintf("session-%d", index)}
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("step %d did not start", index)
		}
		return launcher.prompts[index]
	}
	implement := next(0)
	if err := svc.CompleteAttempt(t.Context(), implement.AttemptID, implement.AgentToken, "built", ""); err != nil {
		t.Fatal(err)
	}
	verify := next(1)
	if implement.Model != "impl/model" || verify.Model != "" {
		t.Fatalf("validator inherited the implementer's model: implement=%q verify=%q", implement.Model, verify.Model)
	}
	if !verify.Verification || !strings.Contains(verify.Criteria, "- [ ] returns 200") {
		t.Fatalf("validator did not get the acceptance criteria: %#v", verify)
	}
	for _, passed := range []bool{false, true} {
		launcher.passed = passed
		svc.forgetVerificationChecks(verify.AttemptID)
		if err := svc.CompleteAttempt(t.Context(), verify.AttemptID, verify.AgentToken, "criteria PASS", ""); !errors.Is(err, ErrVerificationChecksPending) {
			t.Fatalf("completion before checks = %v", err)
		}
		select {
		case <-launcher.notified:
		case <-time.After(5 * time.Second):
			t.Fatal("checks never reported back")
		}
		err := svc.CompleteAttempt(t.Context(), verify.AttemptID, verify.AgentToken, "criteria PASS", "")
		if passed != (err == nil) {
			t.Fatalf("passed=%v completion = %v", passed, err)
		}
	}
	if strings.Join(launcher.commands, ",") != "make test" {
		t.Fatalf("commands = %v", launcher.commands)
	}
	attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		if attempt.ID == verify.AttemptID && (attempt.Result == nil || !strings.Contains(attempt.Result.Summary, "Factory checks passed at abc123: make test")) {
			t.Fatalf("recorded result = %#v", attempt.Result)
		}
	}
	// A retried completion with the agent's original summary stays idempotent.
	if err := svc.CompleteAttempt(t.Context(), verify.AttemptID, verify.AgentToken, "criteria PASS", ""); err != nil {
		t.Fatalf("idempotent completion = %v", err)
	}
}

type blockingChecker struct {
	ImplementationLauncher
	started  chan struct{}
	finished chan error
	notified chan string
}

func (c *blockingChecker) RunVerificationChecks(ctx context.Context, _, _, _ string, _ []string) (bool, string, error) {
	c.started <- struct{}{}
	<-ctx.Done()
	c.finished <- ctx.Err()
	return true, "", nil
}

func (c *blockingChecker) NotifyImplementationSession(_ context.Context, _ PlanningSession, message string) error {
	c.notified <- message
	return nil
}

func newBlockingChecker() *blockingChecker {
	return &blockingChecker{started: make(chan struct{}, 1), finished: make(chan error, 1), notified: make(chan string, 1)}
}

func TestVerificationRunsStopWithTheirAttemptAndTheService(t *testing.T) {
	for _, stopBy := range []string{"prune", "forget", "close"} {
		t.Run(stopBy, func(t *testing.T) {
			checker := newBlockingChecker()
			s := verificationService([]string{"make test"}, nil)
			s.implementation, s.stop = checker, make(chan struct{})
			if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrVerificationChecksPending) {
				t.Fatal(err)
			}
			<-checker.started
			switch stopBy {
			case "prune":
				s.pruneAttemptState(map[string]bool{})
			case "forget":
				s.forgetVerificationChecks("att-1")
			case "close":
				s.Close()
			}
			select {
			case err := <-checker.finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("run ended with %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("check run outlived its attempt")
			}
			s.dispatchWG.Wait()
			select {
			case message := <-checker.notified:
				t.Fatalf("a cancelled run reported to the validator: %s", message)
			default:
			}
			if stopBy == "close" {
				if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrFactoryUnavailable) {
					t.Fatalf("checks after close = %v", err)
				}
			}
		})
	}
}

func TestVerificationFailureRerunsOnNextCompletion(t *testing.T) {
	checker := &checkerFake{passed: false, notified: make(chan struct{}, 2)}
	s := verificationService([]string{"make test"}, checker)
	s.stop = make(chan struct{})
	for range 2 {
		if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrVerificationChecksPending) {
			t.Fatalf("completion = %v, want a (re)run", err)
		}
		<-checker.notified
		s.dispatchWG.Wait()
		if _, err := s.gateVerification(t.Context(), verificationAttempt(), "abc"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("failed result = %v", err)
		}
	}
	if checker.runs != 2 {
		t.Fatalf("runs = %d, want a rerun after the reported failure", checker.runs)
	}
}

func TestSupersededVerificationResultIsDropped(t *testing.T) {
	checker := &checkerFake{passed: true, notified: make(chan struct{}, 1)}
	s := verificationService(nil, checker)
	s.checks = map[string]verificationCheck{"att-1": {head: "newer"}}
	s.runVerificationChecks(t.Context(), checker, verificationAttempt(), "older", []string{"true"})
	if run := s.checks["att-1"]; run.head != "newer" || run.done {
		t.Fatalf("older run overwrote the newer one: %+v", run)
	}
	if len(checker.messages) != 0 {
		t.Fatal("superseded run messaged the validator")
	}
}

func TestNudgeRestartedValidators(t *testing.T) {
	checker := &checkerFake{notified: make(chan struct{}, 4)}
	s := verificationService([]string{"make test"}, checker)
	before := verificationAttempt()
	before.StartedAt = processStart.Add(-time.Minute).UnixMilli()
	after := verificationAttempt()
	after.ID, after.StartedAt = "att-new", processStart.Add(time.Minute).UnixMilli()
	implementation := before
	implementation.ID, implementation.WorkID = "att-impl", "impl-1"
	running := before
	running.ID = "att-running"
	s.checks = map[string]verificationCheck{"att-running": {head: "abc"}}
	alive := []model.FactoryAttempt{before, after, implementation, running}
	s.nudgeRestartedValidators(t.Context(), alive)
	s.nudgeRestartedValidators(t.Context(), alive)
	if len(checker.messages) != 1 || !strings.Contains(checker.messages[0], "call complete_attempt again with attempt_id att-1") {
		t.Fatalf("nudges = %q", checker.messages)
	}
	(&NativeService{store: verificationStoreFake{}}).nudgeRestartedValidators(t.Context(), alive)
}
