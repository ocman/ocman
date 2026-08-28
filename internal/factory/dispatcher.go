package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type DispatchState string

const (
	DispatchReady     DispatchState = "ready"
	DispatchRunning   DispatchState = "running"
	DispatchCompleted DispatchState = "completed"
)

type DispatchItem struct {
	ID         string                      `json:"id"`
	EpicID     string                      `json:"epicId"`
	Title      string                      `json:"title"`
	Repository string                      `json:"repository"`
	State      DispatchState               `json:"state"`
	AttemptID  string                      `json:"attemptId,omitempty"`
	Outcome    model.FactoryAttemptOutcome `json:"outcome,omitempty"`
}

type FactoryExecutionRequest struct {
	AttemptID string
	EpicID    string
	WorkID    string
	Policy    model.FactoryAttemptPolicy
}

type FactoryExecutor interface {
	Execute(context.Context, FactoryExecutionRequest) (model.FactoryAttemptResult, error)
	ReplaySafe() bool
}

type stubFactoryExecutor struct{}

func (stubFactoryExecutor) ReplaySafe() bool { return true }

func (stubFactoryExecutor) Execute(_ context.Context, req FactoryExecutionRequest) (model.FactoryAttemptResult, error) {
	return model.FactoryAttemptResult{SchemaVersion: 1, Summary: "stub execution completed for " + req.WorkID}, nil
}

type dispatchCandidate struct {
	issue  beadsIssue
	epic   WorkEpic
	item   PlanItem
	target PlanTarget
}

func (s *Service) runDispatcher(ctx context.Context) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.attempts == nil {
		return nil
	}
	beadsDir := filepath.Join(s.dir, "beads")
	bd, _, failure := compatibleBeads(ctx, beadsDir, s.runner)
	if failure.Reason != "" {
		return beadsError(failure)
	}
	epics, err := listWorkEpics(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return err
	}
	for i := range epics {
		if epics[i].Plan.State == PlanApproved && epics[i].Plan.Approval != nil {
			if err := validateApprovedPlan(epics[i]); err != nil {
				return err
			}
			if err := s.ensureApprovedPlanIssues(ctx, bd, beadsDir, epics[i]); err != nil {
				return err
			}
		}
	}
	if err := s.reconcileAttempts(ctx, bd, beadsDir); err != nil {
		return err
	}
	for {
		candidate, ok, err := s.nextDispatchCandidate(ctx, bd, beadsDir)
		if err != nil || !ok {
			return err
		}
		if err := s.dispatchCandidate(ctx, bd, beadsDir, candidate); err != nil {
			return err
		}
	}
}

func validateApprovedPlan(epic WorkEpic) error {
	approval := epic.Plan.Approval
	if approval == nil || approval.Revision != epic.Plan.Revision || approval.Hash != epic.Plan.Hash || hashPlanGraph(approval.Graph) != approval.Hash {
		return fmt.Errorf("%w: approved Plan revision or hash does not match its frozen graph", ErrPlanIncompatible)
	}
	return nil
}

func (s *Service) ensureApprovedPlanIssues(ctx context.Context, bd, beadsDir string, epic WorkEpic) error {
	issues, err := listFactoryIssues(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, item := range epic.Plan.Approval.Graph.Items {
		identities := planItemIdentityIssues(issues, epic, item.ID)
		if len(identities) > 1 {
			return fmt.Errorf("%w: Plan item %q was materialized more than once", ErrBeadsFailure, item.ID)
		}
		matches := materializedPlanIssues(issues, epic, item.ID)
		if len(identities) == 1 && len(matches) == 0 {
			return fmt.Errorf("%w: materialized Plan item %q does not match its approved metadata", ErrBeadsFailure, item.ID)
		}
		if len(matches) == 1 {
			ids[item.ID] = matches[0].ID
			continue
		}
		target, ok := planTarget(epic.Plan.Approval.Graph, item.TargetID)
		if !ok {
			return fmt.Errorf("%w: Plan item %q has no approved target", ErrPlanIncompatible, item.ID)
		}
		metadata := approvedPlanItemMetadata(epic, item, target)
		path, cleanup, err := writeJSONTemp(s.dir, "plan-item-*.json", metadata)
		if err != nil {
			return err
		}
		issueType := "task"
		if item.Kind == "gate" {
			issueType = "gate"
		}
		out, createErr := run(ctx, s.runner, bd, s.dir, []string{
			"create", item.Title, "--type", issueType, "--parent", epic.ID, "--priority", "2", "--metadata", "@" + path, "--json",
		}, beadsCommandEnv(beadsDir))
		cleanup()
		created, parseErr := parseIssueEnvelope(out)
		if createErr == nil && parseErr == nil && matchesApprovedPlanItem(created, metadata) {
			ids[item.ID] = created.ID
			continue
		}
		if createErr == nil && parseErr == nil {
			parseErr = errors.New("beads created an issue with unexpected approved Plan metadata")
		}
		issues, err = listFactoryIssues(context.WithoutCancel(ctx), bd, beadsDir, s.runner)
		if err == nil {
			matches = materializedPlanIssues(issues, epic, item.ID)
		}
		if len(matches) != 1 {
			if createErr != nil {
				return fmt.Errorf("%w: materialize Plan item %q: %w", ErrBeadsFailure, item.ID, createErr)
			}
			return fmt.Errorf("%w: materialize Plan item %q: %w", ErrBeadsFailure, item.ID, parseErr)
		}
		ids[item.ID] = matches[0].ID
	}

	issues, err = listFactoryIssues(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return err
	}
	for _, dependency := range epic.Plan.Approval.Graph.Dependencies {
		from, to := ids[dependency.From], ids[dependency.To]
		if from == "" || to == "" {
			return fmt.Errorf("%w: Plan dependency references an unmaterialized item", ErrPlanIncompatible)
		}
		if hasBlockingDependency(issues, from, to) {
			continue
		}
		_, depErr := run(ctx, s.runner, bd, s.dir, []string{"dep", "add", from, to, "--type", "blocks", "--json"}, beadsCommandEnv(beadsDir))
		if depErr == nil {
			continue
		}
		current, listErr := listFactoryIssues(context.WithoutCancel(ctx), bd, beadsDir, s.runner)
		if listErr != nil || !hasBlockingDependency(current, from, to) {
			return fmt.Errorf("%w: materialize Plan dependency: %w", ErrBeadsFailure, depErr)
		}
	}
	return nil
}

func approvedPlanItemMetadata(epic WorkEpic, item PlanItem, target PlanTarget) map[string]string {
	approval := epic.Plan.Approval
	metadata := map[string]string{
		"ocman.contract":           "1",
		"ocman.kind":               item.Kind,
		"ocman.work_epic_id":       epic.ID,
		"ocman.plan_item_id":       item.ID,
		"ocman.plan_revision":      strconv.Itoa(approval.Revision),
		"ocman.plan_hash":          approval.Hash,
		"ocman.target_id":          item.TargetID,
		"ocman.target_repository":  target.Repository,
		"ocman.permission_profile": item.Profile,
		"ocman.formula_id":         approval.FormulaID,
		"ocman.formula_version":    strconv.Itoa(approval.FormulaVersion),
		"ocman.formula_hash":       approval.FormulaHash,
		"ocman.formula_origin":     approval.FormulaOrigin,
		"ocman.instantiation_id":   approval.InstantiationID,
	}
	if item.GateType != "" {
		metadata["ocman.gate_type"] = item.GateType
	}
	return metadata
}

func materializedPlanIssues(issues []beadsIssue, epic WorkEpic, itemID string) []beadsIssue {
	var matches []beadsIssue
	var item PlanItem
	for _, approved := range epic.Plan.Approval.Graph.Items {
		if approved.ID == itemID {
			item = approved
			break
		}
	}
	target, ok := planTarget(epic.Plan.Approval.Graph, item.TargetID)
	if !ok {
		return nil
	}
	expected := approvedPlanItemMetadata(epic, item, target)
	for _, issue := range issues {
		if matchesApprovedPlanItem(issue, expected) {
			matches = append(matches, issue)
		}
	}
	return matches
}

func planItemIdentityIssues(issues []beadsIssue, epic WorkEpic, itemID string) []beadsIssue {
	var matches []beadsIssue
	for _, issue := range issues {
		if issue.Metadata["ocman.work_epic_id"] == epic.ID && issue.Metadata["ocman.plan_item_id"] == itemID &&
			issue.Metadata["ocman.plan_revision"] == strconv.Itoa(epic.Plan.Approval.Revision) && issue.Metadata["ocman.plan_hash"] == epic.Plan.Approval.Hash {
			matches = append(matches, issue)
		}
	}
	return matches
}

func matchesApprovedPlanItem(issue beadsIssue, expected map[string]string) bool {
	if issue.ID == "" {
		return false
	}
	for key, value := range expected {
		if issue.Metadata[key] != value {
			return false
		}
	}
	return true
}

func planTarget(graph PlanGraph, id string) (PlanTarget, bool) {
	for _, target := range graph.Targets {
		if target.ID == id {
			return target, true
		}
	}
	return PlanTarget{}, false
}

func parseIssueEnvelope(data []byte) (beadsIssue, error) {
	var envelope struct {
		SchemaVersion int         `json:"schema_version"`
		Data          *beadsIssue `json:"data"`
	}
	if !decodeOne(data, &envelope) || envelope.SchemaVersion != 1 || envelope.Data == nil || envelope.Data.ID == "" {
		return beadsIssue{}, errors.New("unsupported Beads issue response")
	}
	return *envelope.Data, nil
}

func hasBlockingDependency(issues []beadsIssue, from, to string) bool {
	for _, issue := range issues {
		if issue.ID != from {
			continue
		}
		for _, dependency := range issue.Dependencies {
			if dependency.IssueID == from && dependency.DependsOnID == to && dependency.Type == "blocks" {
				return true
			}
		}
	}
	return false
}

func (s *Service) nextDispatchCandidate(ctx context.Context, bd, beadsDir string) (dispatchCandidate, bool, error) {
	epics, err := listWorkEpics(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return dispatchCandidate{}, false, err
	}
	ready, err := listReadyFactoryIssues(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return dispatchCandidate{}, false, err
	}
	attempts, err := s.attempts.ListFactoryAttempts(ctx, "")
	if err != nil {
		return dispatchCandidate{}, false, err
	}
	attempted := map[string]bool{}
	blockedRepos := map[string]bool{}
	for _, attempt := range attempts {
		attempted[attempt.WorkID] = true
		if attempt.Phase == model.FactoryAttemptTerminal && attempt.Outcome != model.FactoryAttemptSucceeded && attempt.Failure.Type != "claim_failed" {
			blockedRepos[attempt.FrozenPolicy.Repository] = true
		}
	}
	var candidates []dispatchCandidate
	for _, issue := range ready {
		if attempted[issue.ID] {
			continue
		}
		candidate, ok := matchDispatchCandidate(issue, epics)
		if ok && !blockedRepos[candidate.target.Repository] {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].issue.Priority != candidates[j].issue.Priority {
			return candidates[i].issue.Priority < candidates[j].issue.Priority
		}
		if !candidates[i].issue.CreatedAt.Equal(candidates[j].issue.CreatedAt) {
			return candidates[i].issue.CreatedAt.Before(candidates[j].issue.CreatedAt)
		}
		return candidates[i].issue.ID < candidates[j].issue.ID
	})
	if len(candidates) == 0 {
		return dispatchCandidate{}, false, nil
	}
	return candidates[0], true, nil
}

func listReadyFactoryIssues(ctx context.Context, bd, beadsDir string, r runner) ([]beadsIssue, error) {
	out, err := run(ctx, r, bd, parentDir(beadsDir), []string{
		"--readonly", "ready", "--unassigned", "--limit", "0", "--metadata-field", "ocman.contract=1", "--metadata-field", "ocman.kind=agent-work", "--json",
	}, beadsCommandEnv(beadsDir))
	if err != nil {
		return nil, fmt.Errorf("%w: list ready Factory work: %w", ErrBeadsFailure, err)
	}
	var envelope struct {
		SchemaVersion int           `json:"schema_version"`
		Data          *[]beadsIssue `json:"data"`
	}
	if !decodeOne(out, &envelope) || envelope.SchemaVersion != 1 || envelope.Data == nil {
		return nil, fmt.Errorf("%w: Beads returned an unsupported ready-work response", ErrBeadsFailure)
	}
	return *envelope.Data, nil
}

func matchDispatchCandidate(issue beadsIssue, epics []WorkEpic) (dispatchCandidate, bool) {
	if issue.Status != "open" || issue.Priority < 0 || issue.CreatedAt.IsZero() || issue.Metadata["ocman.kind"] != "agent-work" {
		return dispatchCandidate{}, false
	}
	for _, epic := range epics {
		approval := epic.Plan.Approval
		if epic.ID != issue.Metadata["ocman.work_epic_id"] || epic.Plan.State != PlanApproved || approval == nil ||
			issue.Metadata["ocman.plan_revision"] != strconv.Itoa(approval.Revision) || issue.Metadata["ocman.plan_hash"] != approval.Hash {
			continue
		}
		for _, item := range approval.Graph.Items {
			if item.ID != issue.Metadata["ocman.plan_item_id"] || item.Kind != "agent-work" || item.Profile != issue.Metadata["ocman.permission_profile"] {
				continue
			}
			target, ok := planTarget(approval.Graph, item.TargetID)
			if !ok || target.Repository != issue.Metadata["ocman.target_repository"] {
				return dispatchCandidate{}, false
			}
			if !matchesApprovedPlanItem(issue, approvedPlanItemMetadata(epic, item, target)) {
				return dispatchCandidate{}, false
			}
			return dispatchCandidate{issue: issue, epic: epic, item: item, target: target}, true
		}
	}
	return dispatchCandidate{}, false
}

func (s *Service) dispatchCandidate(ctx context.Context, bd, beadsDir string, candidate dispatchCandidate) error {
	if s.activeRepos == nil {
		s.activeRepos = map[string]bool{}
	}
	if s.activeRepos[candidate.target.Repository] {
		return nil
	}
	// Capacity is one for the stub tracer; reserve it and the repository before durable preparation.
	s.activeRepos[candidate.target.Repository] = true
	defer delete(s.activeRepos, candidate.target.Repository)
	policy := model.FactoryAttemptPolicy{
		PlanRevision: candidate.epic.Plan.Approval.Revision,
		PlanHash:     candidate.epic.Plan.Approval.Hash,
		TargetID:     candidate.target.ID,
		Repository:   candidate.target.Repository,
		Profile:      candidate.item.Profile,
	}
	attempt, err := s.attempts.CreatePreparedFactoryAttempt(ctx, candidate.epic.ID, candidate.issue.ID, policy, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := s.auditAttempt(ctx, attempt, "attempt.prepared", map[string]any{"repository": policy.Repository, "profile": policy.Profile}); err != nil {
		return err
	}
	claimed, err := s.claimAttempt(ctx, bd, beadsDir, candidate.issue.ID, attempt.ID)
	if err != nil || !claimed {
		failure := model.FactoryAttemptFailure{Type: "claim_failed", Message: "Beads claim did not commit"}
		if err != nil {
			failure.Message = err.Error()
		}
		_, failErr := s.attempts.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, failure, time.Now().UTC())
		if failErr != nil {
			return failErr
		}
		if err != nil {
			return err
		}
		return nil
	}
	changed, err := s.attempts.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{}, time.Now().UTC())
	if err != nil || !changed {
		return fmt.Errorf("activate Factory attempt %s: changed=%t: %w", attempt.ID, changed, err)
	}
	attempt.Phase = model.FactoryAttemptActive
	return s.executeAttempt(ctx, bd, beadsDir, attempt)
}

func (s *Service) claimAttempt(ctx context.Context, bd, beadsDir, workID, attemptID string) (bool, error) {
	out, claimErr := run(ctx, s.runner, bd, s.dir, []string{
		"update", workID, "--claim", "--set-metadata", "ocman.attempt_id=" + attemptID, "--json",
	}, beadsCommandEnv(beadsDir))
	issue, parseErr := parseIssueEnvelope(out)
	if claimErr == nil && parseErr == nil && issue.ID == workID && issue.Status == "in_progress" && issue.Metadata["ocman.attempt_id"] == attemptID {
		return true, nil
	}
	issues, err := listFactoryIssues(context.WithoutCancel(ctx), bd, beadsDir, s.runner)
	if err != nil {
		if claimErr != nil {
			return false, claimErr
		}
		return false, err
	}
	for _, current := range issues {
		if current.ID == workID {
			if current.Status == "in_progress" && current.Metadata["ocman.attempt_id"] == attemptID {
				return true, nil
			}
			return false, nil
		}
	}
	if claimErr != nil {
		return false, claimErr
	}
	return false, parseErr
}

func (s *Service) executeAttempt(ctx context.Context, bd, beadsDir string, attempt model.FactoryAttempt) error {
	result, err := s.executor.Execute(ctx, FactoryExecutionRequest{AttemptID: attempt.ID, EpicID: attempt.EpicID, WorkID: attempt.WorkID, Policy: attempt.FrozenPolicy})
	if err != nil || result.SchemaVersion != 1 || strings.TrimSpace(result.Summary) == "" {
		failure := model.FactoryAttemptFailure{Type: "stub_execution_failed", Message: "stub executor returned invalid structured completion"}
		if err != nil {
			failure.Message = err.Error()
		}
		_, failErr := s.attempts.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, failure, time.Now().UTC())
		if failErr != nil {
			return failErr
		}
		if metadataErr := s.persistFailedAttempt(context.WithoutCancel(ctx), bd, beadsDir, attempt, failure); metadataErr != nil {
			return metadataErr
		}
		return errors.New(failure.Message)
	}
	changed, err := s.attempts.CompleteFactoryAttempt(ctx, attempt.ID, result, time.Now().UTC())
	if err != nil || !changed {
		return fmt.Errorf("persist Factory completion for %s: changed=%t: %w", attempt.ID, changed, err)
	}
	return s.closeSuccessfulAttempt(ctx, bd, beadsDir, attempt, result)
}

func (s *Service) persistFailedAttempt(ctx context.Context, bd, beadsDir string, attempt model.FactoryAttempt, failure model.FactoryAttemptFailure) error {
	_, err := run(ctx, s.runner, bd, s.dir, []string{
		"update", attempt.WorkID,
		"--set-metadata", "ocman.attempt_id=" + attempt.ID,
		"--set-metadata", "ocman.terminal_outcome=failed",
		"--set-metadata", "ocman.failure_type=" + failure.Type,
		"--json",
	}, beadsCommandEnv(beadsDir))
	if err != nil {
		return fmt.Errorf("%w: persist failed Work Item evidence: %w", ErrBeadsFailure, err)
	}
	return s.auditAttempt(ctx, attempt, "attempt.failed", failure)
}

func (s *Service) closeSuccessfulAttempt(ctx context.Context, bd, beadsDir string, attempt model.FactoryAttempt, result model.FactoryAttemptResult) error {
	_, err := run(ctx, s.runner, bd, s.dir, []string{
		"update", attempt.WorkID,
		"--set-metadata", "ocman.attempt_id=" + attempt.ID,
		"--set-metadata", "ocman.terminal_outcome=succeeded",
		"--json",
	}, beadsCommandEnv(beadsDir))
	if err != nil {
		return fmt.Errorf("%w: persist terminal Work Item evidence: %w", ErrBeadsFailure, err)
	}
	if _, err := run(ctx, s.runner, bd, s.dir, []string{"close", attempt.WorkID, "--reason", result.Summary, "--json"}, beadsCommandEnv(beadsDir)); err != nil {
		return fmt.Errorf("%w: close successful Work Item: %w", ErrBeadsFailure, err)
	}
	return s.auditAttempt(ctx, attempt, "attempt.succeeded", result)
}

func (s *Service) reconcileAttempts(ctx context.Context, bd, beadsDir string) error {
	attempts, err := s.attempts.ListFactoryAttempts(ctx, "")
	if err != nil {
		return err
	}
	issues, err := listFactoryIssues(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return err
	}
	byID := map[string]beadsIssue{}
	byAttempt := map[string]model.FactoryAttempt{}
	for _, issue := range issues {
		byID[issue.ID] = issue
	}
	for _, attempt := range attempts {
		byAttempt[attempt.ID] = attempt
	}
	for _, issue := range issues {
		if issue.Status == "in_progress" && issue.Metadata["ocman.plan_item_id"] != "" {
			attemptID := issue.Metadata["ocman.attempt_id"]
			if attemptID == "" {
				return fmt.Errorf("recordless Factory claim on %s", issue.ID)
			}
			if _, ok := byAttempt[attemptID]; !ok {
				return fmt.Errorf("factory claim %s references missing attempt %s", issue.ID, attemptID)
			}
		}
	}
	for _, attempt := range attempts {
		issue, ok := byID[attempt.WorkID]
		if !ok {
			return fmt.Errorf("factory attempt %s references missing Work Item %s", attempt.ID, attempt.WorkID)
		}
		switch attempt.Phase {
		case model.FactoryAttemptPrepared:
			if issue.Status != "in_progress" || issue.Metadata["ocman.attempt_id"] != attempt.ID {
				_, err := s.attempts.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "claim_failed", Message: "prepared claim was not committed"}, time.Now().UTC())
				if err != nil {
					return err
				}
				continue
			}
			changed, err := s.attempts.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{}, time.Now().UTC())
			if err != nil || !changed {
				return fmt.Errorf("recover prepared Factory attempt %s: changed=%t: %w", attempt.ID, changed, err)
			}
			attempt.Phase = model.FactoryAttemptActive
			if err := s.executeAttempt(ctx, bd, beadsDir, attempt); err != nil {
				return err
			}
		case model.FactoryAttemptActive:
			if issue.Status != "in_progress" || issue.Metadata["ocman.attempt_id"] != attempt.ID {
				return fmt.Errorf("active Factory attempt %s no longer owns Work Item %s", attempt.ID, attempt.WorkID)
			}
			if !s.executor.ReplaySafe() {
				return fmt.Errorf("active Factory attempt %s has no replay-safe executor", attempt.ID)
			}
			if err := s.executeAttempt(ctx, bd, beadsDir, attempt); err != nil {
				return err
			}
		case model.FactoryAttemptTerminal:
			if attempt.Outcome == model.FactoryAttemptSucceeded {
				if issue.Metadata["ocman.attempt_id"] != attempt.ID || attempt.Result == nil {
					return fmt.Errorf("successful Factory attempt %s has incomplete closure evidence", attempt.ID)
				}
				if issue.Status != "closed" {
					if err := s.closeSuccessfulAttempt(ctx, bd, beadsDir, attempt, *attempt.Result); err != nil {
						return err
					}
				} else if err := s.auditAttempt(ctx, attempt, "attempt.succeeded", *attempt.Result); err != nil {
					return err
				}
			}
			if attempt.Outcome == model.FactoryAttemptFailed {
				if attempt.Failure.Type == "claim_failed" {
					if issue.Status == "in_progress" && issue.Metadata["ocman.attempt_id"] == attempt.ID {
						return fmt.Errorf("failed Factory claim %s unexpectedly owns Work Item %s", attempt.ID, attempt.WorkID)
					}
					continue
				}
				if issue.Metadata["ocman.attempt_id"] != attempt.ID {
					return fmt.Errorf("failed Factory attempt %s has incomplete claim evidence", attempt.ID)
				}
				failure := attempt.Failure
				if issue.Metadata["ocman.terminal_outcome"] != "failed" || issue.Metadata["ocman.failure_type"] != failure.Type {
					if err := s.persistFailedAttempt(ctx, bd, beadsDir, attempt, failure); err != nil {
						return err
					}
				} else if err := s.auditAttempt(ctx, attempt, "attempt.failed", failure); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) auditAttempt(ctx context.Context, attempt model.FactoryAttempt, action string, details any) error {
	return s.attempts.AppendFactoryAuditOnce(ctx, model.AuditRecord{
		EpicID: attempt.EpicID, WorkID: attempt.WorkID, AttemptID: attempt.ID,
		Actor: "factory", Action: action, Details: details, At: time.Now().UTC(),
	})
}

func (s *Service) dispatchStatus(ctx context.Context, bd, beadsDir string) ([]DispatchItem, error) {
	issues, err := listFactoryIssues(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return nil, err
	}
	ready, err := listReadyFactoryIssues(ctx, bd, beadsDir, s.runner)
	if err != nil {
		return nil, err
	}
	readyIDs := map[string]bool{}
	for _, issue := range ready {
		readyIDs[issue.ID] = true
	}
	attempts, err := s.attempts.ListFactoryAttempts(ctx, "")
	if err != nil {
		return nil, err
	}
	latest := map[string]model.FactoryAttempt{}
	for _, attempt := range attempts {
		if current, ok := latest[attempt.WorkID]; !ok || attempt.Sequence > current.Sequence {
			latest[attempt.WorkID] = attempt
		}
	}
	items := make([]DispatchItem, 0)
	for _, issue := range issues {
		if issue.Metadata["ocman.kind"] != "agent-work" || issue.Metadata["ocman.plan_item_id"] == "" {
			continue
		}
		item := DispatchItem{ID: issue.ID, EpicID: issue.Metadata["ocman.work_epic_id"], Title: issue.Title, Repository: issue.Metadata["ocman.target_repository"]}
		if attempt, ok := latest[issue.ID]; ok {
			item.AttemptID, item.Outcome = attempt.ID, attempt.Outcome
			if attempt.Phase == model.FactoryAttemptTerminal {
				item.State = DispatchCompleted
			} else {
				item.State = DispatchRunning
			}
		} else if readyIDs[issue.ID] {
			item.State = DispatchReady
		} else {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}
