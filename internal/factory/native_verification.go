package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/sirupsen/logrus"
)

// ErrVerificationChecksPending means ocman started (or is still running) the
// Formula-declared checks; the results arrive in the validator's session.
var ErrVerificationChecksPending = errors.New("factory verification checks are running")

// VerificationChecker is implemented by launchers that can run Formula
// verification commands in the shared worktree and message the validator.
type VerificationChecker interface {
	RunVerificationChecks(ctx context.Context, repo, branch, target string, commands []string) (passed bool, report string, err error)
	NotifyImplementationSession(ctx context.Context, session PlanningSession, message string) error
}

// ponytail: one ceiling for the whole command block; per-command limits
// belong in the Formula once someone needs them.
var verificationChecksTimeout = time.Hour

// processStart separates validators that may have been waiting on checks
// lost in a restart from ones that started after it.
var processStart = time.Now()

type verificationCheck struct {
	head   string
	done   bool
	passed bool
	cancel context.CancelFunc
}

func (s *NativeService) verificationStep(ctx context.Context, attempt model.FactoryAttempt) (*model.WorkflowStep, error) {
	issues, err := s.store.ListFactoryIssues(ctx, attempt.EpicID)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if issue.ID == attempt.WorkID && issue.Workflow != nil && issue.Workflow.Kind == "verification" {
			return issue.Workflow, nil
		}
	}
	return nil, nil
}

// gateVerification enforces the validator contract for one completion call:
// the validator may not move the shared branch, and Formula commands must
// have passed at this exact HEAD. It returns the summary suffix recording
// the passed checks. A failed run is reported once and then forgotten, so
// the next completion (a flaky rerun, or after a human resume) runs again.
func (s *NativeService) gateVerification(ctx context.Context, attempt model.FactoryAttempt, head string) (string, error) {
	step, err := s.verificationStep(ctx, attempt)
	if err != nil || step == nil {
		return "", err
	}
	if checkpoint := attempt.FrozenPolicy.CheckpointSHA; checkpoint != "" && head != checkpoint {
		return "", fmt.Errorf("%w: verification must not change the shared branch (expected %s, found %s); report problems through request_recovery", ErrInvalidRequest, checkpoint, head)
	}
	commands := step.Config.Commands
	if len(commands) == 0 {
		return "", nil
	}
	checker, ok := s.implementation.(VerificationChecker)
	if !ok {
		return "", fmt.Errorf("%w: verification commands are unavailable", ErrFactoryUnavailable)
	}
	select {
	case <-s.stop:
		return "", ErrFactoryUnavailable
	default:
	}
	s.checksMu.Lock()
	defer s.checksMu.Unlock()
	if s.checks == nil {
		s.checks = map[string]verificationCheck{}
	}
	run, found := s.checks[attempt.ID]
	if found && run.head == head {
		switch {
		case !run.done:
			return "", ErrVerificationChecksPending
		case run.passed:
			return "\n\nFactory checks passed at " + head + ": " + strings.Join(commands, "; "), nil
		default:
			delete(s.checks, attempt.ID)
			return "", fmt.Errorf("%w: Factory checks failed at %s; call request_recovery with the failures instead of completing", ErrInvalidRequest, head)
		}
	}
	if found && run.cancel != nil {
		run.cancel()
	}
	runCtx, cancel := context.WithTimeout(context.Background(), verificationChecksTimeout)
	s.checks[attempt.ID] = verificationCheck{head: head, cancel: cancel}
	s.dispatchWG.Add(1)
	go func() {
		defer s.dispatchWG.Done()
		defer cancel()
		// Shutdown kills the command tree instead of orphaning it.
		go func() {
			select {
			case <-s.stop:
				cancel()
			case <-runCtx.Done():
			}
		}()
		s.runVerificationChecks(runCtx, checker, attempt, head, commands)
	}()
	return "", ErrVerificationChecksPending
}

func (s *NativeService) runVerificationChecks(ctx context.Context, checker VerificationChecker, attempt model.FactoryAttempt, head string, commands []string) {
	policy := attempt.FrozenPolicy
	passed, report, err := checker.RunVerificationChecks(ctx, policy.Repository, policy.Branch, policy.TargetBranch, commands)
	if err != nil {
		passed, report = false, "Factory could not run the checks: "+err.Error()
	}
	s.checksMu.Lock()
	current, ok := s.checks[attempt.ID]
	// A newer run, a finished attempt, or shutdown superseded this result.
	superseded := !ok || current.head != head || current.done
	switch {
	case superseded:
	case errors.Is(ctx.Err(), context.Canceled):
		// Cancelled with the attempt or service: nobody is owed a result.
		delete(s.checks, attempt.ID)
		superseded = true
	default:
		s.checks[attempt.ID] = verificationCheck{head: head, done: true, passed: passed}
	}
	s.checksMu.Unlock()
	if superseded {
		return
	}
	if err := checker.NotifyImplementationSession(context.WithoutCancel(ctx), attempt.Session, verificationResultMessage(attempt.ID, head, passed, report)); err != nil {
		// The idle watchdog pauses the attempt if the validator never hears back.
		logrus.WithError(err).WithField("attempt", attempt.ID).Warn("Factory could not deliver verification check results")
	}
}

// pruneAttemptState drops per-attempt memory for attempts that are no longer
// active and stops their check runs, so a retried or cancelled attempt never
// shares the worktree with its predecessor's commands.
func (s *NativeService) pruneAttemptState(active map[string]bool) {
	s.checksMu.Lock()
	defer s.checksMu.Unlock()
	for id, run := range s.checks {
		if !active[id] {
			if run.cancel != nil {
				run.cancel()
			}
			delete(s.checks, id)
		}
	}
	for id := range s.idleProbedAt {
		if !active[id] {
			delete(s.idleProbedAt, id)
		}
	}
}

// nudgeRestartedValidators tells validators that may have been waiting for
// check results lost in a restart to ask again. Once per attempt per process.
func (s *NativeService) nudgeRestartedValidators(ctx context.Context, alive []model.FactoryAttempt) {
	checker, ok := s.implementation.(VerificationChecker)
	if !ok {
		return
	}
	for _, attempt := range alive {
		if !time.UnixMilli(attempt.StartedAt).Before(processStart) {
			continue
		}
		s.checksMu.Lock()
		_, running := s.checks[attempt.ID]
		nudged := s.nudged[attempt.ID]
		if s.nudged == nil {
			s.nudged = map[string]bool{}
		}
		s.nudged[attempt.ID] = true
		s.checksMu.Unlock()
		if running || nudged {
			continue
		}
		if step, err := s.verificationStep(ctx, attempt); err != nil || step == nil || len(step.Config.Commands) == 0 {
			continue
		}
		message := "Factory restarted. If you were waiting for Factory check results, they were lost: call complete_attempt again with attempt_id " + attempt.ID + " and the same attempt_token to rerun them. Otherwise continue."
		if err := checker.NotifyImplementationSession(ctx, attempt.Session, message); err != nil {
			logrus.WithError(err).WithField("attempt", attempt.ID).Warn("Factory could not nudge a restarted validator")
		}
	}
}

func verificationResultMessage(attemptID, head string, passed bool, report string) string {
	verdict, next := "FAILED", "Do not claim success. Call request_recovery with attempt_id "+attemptID+" and the same attempt_token, and summarize which checks and acceptance criteria failed."
	if passed {
		verdict, next = "PASSED", "Weigh these results together with your own review. If every acceptance criterion holds, call complete_attempt again with attempt_id "+attemptID+" and the same attempt_token, listing each acceptance criterion as PASS or FAIL in the summary. Otherwise call request_recovery."
	}
	return fmt.Sprintf("Factory verification checks at %s: %s\n\n%s\n\n%s", head, verdict, report, next)
}

// verificationCriteria lists the project's implementation Issues so the
// validator checks the contract written at planning time, not its own idea
// of done. Descriptions carry the acceptance criteria folded in at submit.
func (s *NativeService) verificationCriteria(ctx context.Context, epicID, project string) string {
	issues, err := s.store.ListFactoryIssues(ctx, epicID)
	if err != nil {
		return "(unavailable: " + err.Error() + ")"
	}
	var out strings.Builder
	for _, issue := range issues {
		stepKind := ""
		if issue.Workflow != nil {
			stepKind = issue.Workflow.Kind
		}
		if (issue.Kind != "implementation" && issue.Kind != "task") || (stepKind != "" && stepKind != "implementation") || issue.Requirement == "reference" || issue.Project != project {
			continue
		}
		fmt.Fprintf(&out, "\n### %s — %s (%s)\n%s\n", issue.ID, issue.Title, issue.Status, strings.TrimSpace(issue.Description))
	}
	if out.Len() == 0 {
		return "(none recorded)"
	}
	return out.String()
}

func (s *NativeService) forgetVerificationChecks(attemptID string) {
	s.checksMu.Lock()
	if run, ok := s.checks[attemptID]; ok && run.cancel != nil {
		run.cancel()
	}
	delete(s.checks, attemptID)
	delete(s.idleProbedAt, attemptID)
	s.checksMu.Unlock()
}

// foldAcceptanceCriteria requires a verifiable contract on every executable
// implementation node and moves it into the description as a checklist.
func foldAcceptanceCriteria(node *ManifestNode) error {
	criteria := node.AcceptanceCriteria
	node.AcceptanceCriteria = nil
	if node.Type != "implementation" || node.Requirement == "reference" {
		if len(criteria) != 0 {
			return errors.New("acceptanceCriteria belong to implementation work")
		}
		return nil
	}
	if len(criteria) == 0 || len(criteria) > 20 {
		return errors.New("acceptanceCriteria requires 1–20 verifiable criteria")
	}
	var list strings.Builder
	for _, criterion := range criteria {
		criterion = strings.Join(strings.Fields(criterion), " ")
		if criterion == "" || len(criterion) > 1000 {
			return errors.New("each acceptance criterion must be 1–1000 bytes")
		}
		list.WriteString("\n- [ ] " + criterion)
	}
	node.Description = strings.TrimSpace(node.Description + "\n\nAcceptance criteria:" + list.String())
	return nil
}
