package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (s *NativeService) CreateWorkEpic(ctx context.Context, req CreateWorkEpicRequest) (WorkEpic, error) {
	if strings.TrimSpace(req.Goal) == "" || strings.TrimSpace(req.InitialProject) == "" {
		return WorkEpic{}, fmt.Errorf("%w: goal and initialProject are required", ErrInvalidRequest)
	}
	// The goal is the Epic's display name everywhere, so it has to stay a
	// title: one line, short enough to read in a table row. Detail belongs
	// in the brief.
	req.Goal = strings.Join(strings.Fields(req.Goal), " ")
	if len([]rune(req.Goal)) > maxGoalRunes {
		return WorkEpic{}, fmt.Errorf("%w: goal must be a short clear title of at most %d characters; move the detail into brief", ErrInvalidRequest, maxGoalRunes)
	}
	epicID := strings.ToLower(strings.TrimSpace(req.EpicID))
	if epicID != "" && !epicIDPattern.MatchString(epicID) {
		return WorkEpic{}, fmt.Errorf("%w: epicId must be 2-40 characters of lowercase letters, digits and dashes", ErrInvalidRequest)
	}
	project, err := s.canonicalProject(ctx, req.InitialProject)
	if err != nil {
		return WorkEpic{}, err
	}
	req.InitialProject = project
	if !req.AcknowledgeLocalExecution {
		return WorkEpic{}, ErrAcknowledgementRequired
	}
	acks, ok := s.store.(localExecutionAckStore)
	if !ok {
		return WorkEpic{}, ErrFactoryUnavailable
	}
	if err := acks.UpsertFactoryLocalExecutionAck(ctx, "local", project, "factory-implement", "v1", "operator", time.Now()); err != nil {
		return WorkEpic{}, fmt.Errorf("%w: record local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}
	secondary := make([]string, 0, len(req.Projects))
	seen := map[string]bool{project: true}
	for _, admission := range req.Projects {
		if admission.RemoteID != "" && admission.RemoteID != "local" {
			return WorkEpic{}, fmt.Errorf("%w: project %q belongs to remote host %q; only local projects can be admitted", ErrInvalidRequest, admission.Path, admission.RemoteID)
		}
		canonical, err := s.canonicalProject(ctx, admission.Path)
		if err != nil {
			return WorkEpic{}, fmt.Errorf("secondary project %q: %w", admission.Path, err)
		}
		if seen[canonical] {
			return WorkEpic{}, fmt.Errorf("%w: duplicate project %q", ErrInvalidRequest, canonical)
		}
		if !admission.AcknowledgeLocalExecution {
			return WorkEpic{}, fmt.Errorf("%w for project %q", ErrAcknowledgementRequired, canonical)
		}
		if err := acks.UpsertFactoryLocalExecutionAck(ctx, "local", canonical, "factory-implement", "v1", "operator", time.Now()); err != nil {
			return WorkEpic{}, fmt.Errorf("%w: record local execution acknowledgement for %q: %w", ErrFactoryUnavailable, canonical, err)
		}
		seen[canonical] = true
		secondary = append(secondary, canonical)
	}
	formulaID, revision := req.FormulaID, req.FormulaRevision
	if formulaID == "" {
		builtIn := BuiltInTracerFormula()
		formulaID, revision = builtIn.ID, builtIn.Version
	}
	formula, err := s.nativeFormula(ctx, formulaID, revision)
	if err != nil {
		return WorkEpic{}, err
	}
	modelRules := make([]model.PermissionRule, len(req.PermissionRules))
	for i, rule := range req.PermissionRules {
		modelRules[i] = model.PermissionRule{Permission: rule.Permission, Pattern: rule.Pattern, Action: rule.Action}
	}
	store, ok := s.store.(configuredEpicStore)
	if !ok {
		return WorkEpic{}, ErrFactoryUnavailable
	}
	epic, err := store.CreateFactoryEpicWithProjectsAndPermissionRules(ctx, epicID, req.Goal, req.Brief, req.InitialProject, req.InstantiationID, formula, secondary, modelRules)
	switch {
	case errors.Is(err, model.ErrNativeInstantiationConflict):
		err = ErrInstantiationConflict
	case errors.Is(err, model.ErrNativeEpicIDTaken):
		err = fmt.Errorf("%w: %q", ErrEpicIDTaken, epicID)
	}
	if err != nil {
		return WorkEpic{}, err
	}
	return nativeEpic(epic), nil
}

func (s *NativeService) RemoveWorkEpicProject(ctx context.Context, epicID, path string) error {
	store, ok := s.store.(nativeProjectSetStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	project, err := s.canonicalProject(ctx, path)
	if err != nil {
		if !filepath.IsAbs(path) {
			return err
		}
		project = filepath.Clean(path)
	}
	err = store.RemoveFactoryEpicProject(ctx, epicID, project)
	return err
}

func (s *NativeService) ListWorkEpics(ctx context.Context) ([]WorkEpic, error) {
	epics, err := s.store.ListFactoryEpics(ctx)
	result := nativeEpics(epics)
	if store, ok := s.store.(nativePlanningStore); ok {
		for i := range result {
			if attempts, attemptsErr := store.ListFactoryAttempts(ctx, result[i].ID); attemptsErr == nil {
				result[i].Attempts = attempts
			}
			if gate, gateErr := store.GetFactoryPlanGate(ctx, result[i].ID); gateErr == nil {
				decoded := nativePlanGate(gate)
				result[i].PlanGate = &decoded
			}
		}
	}
	for i := range result {
		issues, issuesErr := s.store.ListFactoryIssues(ctx, result[i].ID)
		if issuesErr != nil {
			return nil, issuesErr
		}
		result[i].Progress = factoryProgress(issues)
		result[i].Progress.Stuck = result[i].Progress.Stuck && result[i].Status == "open"
	}
	return result, err
}

func (s *NativeService) CloseMol(ctx context.Context, epicID, molID string) error {
	store, ok := s.store.(nativeClosureStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.CloseFactoryMol(ctx, epicID, molID)
}

func (s *NativeService) CloseEpic(ctx context.Context, epicID string, force bool) error {
	store, ok := s.store.(nativeClosureStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.CloseFactoryEpic(ctx, epicID, force)
}

func (s *NativeService) SetEpicPaused(ctx context.Context, epicID string, paused bool) error {
	store, ok := s.store.(nativeEpicLifecycleStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	if err := store.SetFactoryEpicPaused(ctx, epicID, paused); err != nil {
		return err
	}
	if !paused {
		if err := s.Dispatch(ctx); err != nil {
			select {
			case s.dispatchWake <- struct{}{}:
			default:
			}
		}
	}
	return nil
}

// ReopenIssue returns failed or cancelled work to the queue and dispatches.
func (s *NativeService) ReopenIssue(ctx context.Context, epicID, issueID string) error {
	store, ok := s.store.(nativeReopenStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	if err := store.ReopenFactoryIssue(ctx, epicID, issueID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err := s.Dispatch(ctx); err != nil {
		select {
		case s.dispatchWake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *NativeService) MutateGraph(ctx context.Context, mutation GraphMutation) error {
	store, ok := s.store.(nativeMutationStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	epic, err := s.store.GetFactoryEpic(ctx, mutation.EpicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		return ErrWorkEpicNotFound
	}
	if err != nil {
		return err
	}
	if mutation.Action == "create" || mutation.Project != "" {
		mutation.Project, err = s.canonicalIssueProject(ctx, epic, mutation.Project)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
	}
	err = store.MutateFactoryGraph(ctx, mutation)
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return err
}

func (s *NativeService) canonicalIssueProject(ctx context.Context, epic model.NativeEpic, target string) (string, error) {
	if target == "" {
		target = epic.InitialProject
	}
	canonical, err := s.canonicalProject(ctx, target)
	if err != nil {
		return "", fmt.Errorf("project target %q is invalid: %w", target, err)
	}
	projects := epic.Projects
	if len(projects) == 0 {
		projects = []model.EpicProject{{Path: epic.InitialProject}}
	}
	admitted := make([]string, 0, len(projects))
	for _, project := range projects {
		admitted = append(admitted, project.Path)
		if project.Path == canonical {
			return canonical, nil
		}
	}
	return "", fmt.Errorf("project target %q is not admitted to Epic %s; admitted projects: %s", canonical, epic.ID, strings.Join(admitted, ", "))
}

func factoryProgress(issues []model.NativeIssue) FactoryProgress {
	byID := make(map[string]model.NativeIssue, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
	}
	var progress FactoryProgress
	requirements := make(map[string]string, len(issues))
	movable := false
	for _, issue := range issues {
		if issue.Kind == "mol" {
			continue
		}
		switch {
		case issue.Status == "in_progress" || issue.Status == "retry_wait",
			issue.Kind == "gate" && issue.Status == "open",
			issue.DispatchState == "ready" && issue.Kind != "gate":
			movable = true
		}
		requirement := issue.Requirement
		for parent := issue.ParentID; requirement != "reference" && parent != ""; parent = byID[parent].ParentID {
			if byID[parent].Requirement == "reference" {
				requirement = "reference"
				break
			}
			if byID[parent].Requirement == "optional" {
				requirement = "optional"
			}
		}
		requirements[issue.ID] = requirement
		switch requirement {
		case "reference":
			continue
		case "optional":
			if issue.Status != "closed" {
				progress.OptionalOpen++
			}
		default:
			progress.RequiredTotal++
			if issue.Status == "closed" && issue.Outcome == "succeeded" && (issue.Kind != "gate" || issue.GateResolution == "approved") {
				progress.RequiredSucceeded++
			} else {
				progress.ClosureBlockers = append(progress.ClosureBlockers, issue.Title)
			}
		}
	}
	progress.Stuck = len(progress.ClosureBlockers) > 0 && !movable
	requiredDelivery := map[string]bool{}
	for _, issue := range issues {
		if requirements[issue.ID] != "reference" && (issue.Kind == "implementation" || issue.Kind == "task") && issue.DispatchState != "not_applicable" {
			requiredDelivery[issue.Project] = true
		}
	}
	deliveryProjects := map[string]bool{}
	allDelivered := true
	var deliveryIssues []model.NativeIssue
	for _, issue := range issues {
		if issue.Kind != "delivery" {
			continue
		}
		deliveryIssues = append(deliveryIssues, issue)
	}
	sort.Slice(deliveryIssues, func(i, j int) bool {
		if deliveryIssues[i].Project != deliveryIssues[j].Project {
			return deliveryIssues[i].Project < deliveryIssues[j].Project
		}
		if deliveryIssues[i].CreatedAt != deliveryIssues[j].CreatedAt {
			return deliveryIssues[i].CreatedAt < deliveryIssues[j].CreatedAt
		}
		return deliveryIssues[i].ID < deliveryIssues[j].ID
	})
	lineages := map[string]int{}
	for _, issue := range deliveryIssues {
		status := issue.DispatchState
		if issue.Status == "closed" && issue.Outcome == "succeeded" {
			status = "ready_for_review"
		} else {
			allDelivered = false
			if issue.Status == "closed" {
				status = issue.Outcome
			} else if issue.Status != "open" {
				status = issue.Status
			}
		}
		requiredDelivery[issue.Project] = true
		deliveryProjects[issue.Project] = true
		lineages[issue.Project]++
		progress.ProjectDeliveries = append(progress.ProjectDeliveries, ProjectDeliveryStatus{Project: issue.Project, IssueID: issue.ID, Status: status, Lineage: lineages[issue.Project]})
	}
	for project := range requiredDelivery {
		if !deliveryProjects[project] {
			allDelivered = false
			progress.ProjectDeliveries = append(progress.ProjectDeliveries, ProjectDeliveryStatus{Project: project, Status: "pending"})
		}
	}
	sort.Slice(progress.ProjectDeliveries, func(i, j int) bool {
		if progress.ProjectDeliveries[i].Project != progress.ProjectDeliveries[j].Project {
			return progress.ProjectDeliveries[i].Project < progress.ProjectDeliveries[j].Project
		}
		return progress.ProjectDeliveries[i].Lineage < progress.ProjectDeliveries[j].Lineage
	})
	if len(requiredDelivery) > 0 {
		progress.DeliveryStatus = "pending"
		if allDelivered {
			progress.DeliveryStatus = "ready_for_review"
		}
	}
	return progress
}

func (s *NativeService) GetWorkEpic(ctx context.Context, id string) (WorkEpic, error) {
	epic, err := s.store.GetFactoryEpic(ctx, id)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	result := nativeEpic(epic)
	if err != nil {
		return result, err
	}
	if store, ok := s.store.(nativePlanningStore); ok {
		if attempts, attemptsErr := store.ListFactoryAttempts(ctx, id); attemptsErr == nil {
			result.Attempts = attempts
		}
		if proposal, proposalErr := store.GetFactoryProposalRevision(ctx, id, 0); proposalErr == nil {
			if decoded, decodeErr := nativeProposal(proposal); decodeErr == nil {
				result.Proposal = &decoded
			}
		}
		if gate, gateErr := store.GetFactoryPlanGate(ctx, id); gateErr == nil {
			decoded := nativePlanGate(gate)
			result.PlanGate = &decoded
		}
	}
	issues, issuesErr := s.store.ListFactoryIssues(ctx, id)
	if issuesErr != nil {
		return result, issuesErr
	}
	result.Progress = factoryProgress(issues)
	result.Progress.Stuck = result.Progress.Stuck && result.Status == "open"
	return result, nil
}
