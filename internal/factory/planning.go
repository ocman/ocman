package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

const (
	planMetadataKey   = "ocman.plan"
	planSchemaVersion = 1
)

type PlanState string

const (
	PlanDraft     PlanState = "draft"
	PlanApproved  PlanState = "approved"
	PlanRejected  PlanState = "rejected"
	PlanCancelled PlanState = "cancelled"
)

type Plan struct {
	SchemaVersion    int            `json:"schemaVersion"`
	Revision         int            `json:"revision"`
	Hash             string         `json:"hash"`
	State            PlanState      `json:"state"`
	Draft            PlanGraph      `json:"graph"`
	Planning         []PlanningWork `json:"planning"`
	Approval         *PlanApproval  `json:"approval,omitempty"`
	LastDecision     *PlanDecision  `json:"lastDecision,omitempty"`
	PendingOperation *PlanOperation `json:"pendingOperation,omitempty"`
	LastOperation    *PlanOperation `json:"lastOperation,omitempty"`
	Validation       []string       `json:"validation"`
}

type PlanOperation struct {
	Action       string      `json:"action"`
	FromRevision int         `json:"fromRevision"`
	FromHash     string      `json:"fromHash"`
	Actor        string      `json:"actor"`
	Graph        *PlanGraph  `json:"graph,omitempty"`
	Target       *PlanTarget `json:"target,omitempty"`
	WorkID       string      `json:"workId,omitempty"`
}

type PlanDecision struct {
	Action       string `json:"action"`
	FromRevision int    `json:"fromRevision"`
	Revision     int    `json:"revision"`
	Hash         string `json:"hash"`
	Actor        string `json:"actor"`
	Reason       string `json:"reason,omitempty"`
}

type PlanGraph struct {
	Intent       string           `json:"intent"`
	Targets      []PlanTarget     `json:"targets"`
	Items        []PlanItem       `json:"items"`
	Dependencies []PlanDependency `json:"dependencies"`
}

type PlanTarget struct {
	ID           string       `json:"id"`
	HostID       string       `json:"hostId"`
	Repository   string       `json:"repository"`
	DeliveryBase DeliveryBase `json:"deliveryBase"`
}

type DeliveryBase struct {
	Remote     string `json:"remote"`
	BaseBranch string `json:"baseBranch"`
	BaseSHA    string `json:"baseSha"`
}

type PlanItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	TargetID string `json:"targetId,omitempty"`
	Profile  string `json:"profile,omitempty"`
	GateType string `json:"gateType,omitempty"`
}

type PlanDependency struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type PlanningSession = model.PlanningSession

type PlanningWork struct {
	ID                string          `json:"id"`
	TargetID          string          `json:"targetId"`
	Repository        string          `json:"repository"`
	Status            string          `json:"status"`
	Outcome           string          `json:"outcome,omitempty"`
	CompletedRevision int             `json:"completedRevision,omitempty"`
	CompletedHash     string          `json:"completedHash,omitempty"`
	Session           PlanningSession `json:"session"`
	metadata          map[string]string
}

type PlanApproval struct {
	Revision        int       `json:"revision"`
	Hash            string    `json:"hash"`
	Actor           string    `json:"actor"`
	ApprovedAt      time.Time `json:"approvedAt"`
	FormulaID       string    `json:"formulaId"`
	FormulaVersion  int       `json:"formulaVersion"`
	FormulaHash     string    `json:"formulaHash"`
	FormulaOrigin   string    `json:"formulaOrigin"`
	InstantiationID string    `json:"instantiationId"`
	Reason          string    `json:"reason,omitempty"`
	Graph           PlanGraph `json:"graph"`
}

type PlanningSessionRequest struct {
	EpicID     string
	WorkID     string
	Repository string
	Title      string
}

type PlanningLauncher interface {
	LaunchPlanningSession(context.Context, PlanningSessionRequest) (PlanningSession, error)
	ProbePlanningSession(context.Context, PlanningSession) (bool, error)
	StopPlanningSession(context.Context, PlanningSession) error
}

type FactoryAuditRecord = model.AuditRecord

type MutatePlanRequest struct {
	ExpectedRevision int       `json:"expectedRevision"`
	Graph            PlanGraph `json:"graph"`
}

type PlanMutationResult struct {
	Stale bool `json:"stale"`
	Plan  Plan `json:"plan"`
}

type AddPlanningWorkRequest struct {
	ExpectedRevision          int        `json:"expectedRevision"`
	Target                    PlanTarget `json:"target"`
	AcknowledgeLocalExecution bool       `json:"acknowledgeLocalExecution"`
}

type PlanDecisionRequest struct {
	ExpectedRevision          int    `json:"expectedRevision"`
	ExpectedHash              string `json:"expectedHash"`
	Actor                     string `json:"actor"`
	Reason                    string `json:"reason,omitempty"`
	AcknowledgeLocalExecution bool   `json:"acknowledgeLocalExecution,omitempty"`
}

type CompletePlanningWorkRequest struct {
	ExpectedRevision int    `json:"expectedRevision"`
	ExpectedHash     string `json:"expectedHash"`
	Actor            string `json:"actor"`
}

var ErrPlanNotApprovable = errors.New("plan is not approvable")
var ErrPlanIncompatible = errors.New("plan metadata is incompatible")

type PlanConflictError struct{ Current Plan }

func (e *PlanConflictError) Error() string {
	return fmt.Sprintf("Plan revision conflict: current revision is %d with hash %s", e.Current.Revision, e.Current.Hash)
}

func (e *PlanConflictError) Unwrap() error { return ErrPlanNotApprovable }

func newInitialPlan(epic WorkEpic) Plan {
	graph := PlanGraph{Intent: epic.Goal, Targets: []PlanTarget{{ID: "initial", HostID: localHostID, Repository: epic.InitialProject}}}
	plan := Plan{
		SchemaVersion: planSchemaVersion, Revision: 1, Hash: hashPlanGraph(graph), State: PlanDraft, Draft: graph,
		Planning: []PlanningWork{{ID: epic.Planning.WorkID, TargetID: "initial", Repository: epic.InitialProject, Status: epic.Planning.WorkStatus}},
	}
	plan.Validation = validateComplete(plan)
	return plan
}

func hashPlanGraph(graph PlanGraph) string {
	encoded, _ := json.Marshal(graph)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Service) GetPlan(ctx context.Context, epicID string) (Plan, error) {
	epic, err := s.GetWorkEpic(ctx, epicID)
	if err != nil {
		return Plan{}, err
	}
	if epic.PlanError != "" {
		return Plan{}, fmt.Errorf("%w: %s", ErrPlanIncompatible, epic.PlanError)
	}
	return epic.Plan, nil
}

func (s *Service) MutatePlan(ctx context.Context, epicID string, req MutatePlanRequest) (PlanMutationResult, error) {
	s.pourMu.Lock()
	defer s.pourMu.Unlock()
	if err := s.requireMutationStore(ctx); err != nil {
		return PlanMutationResult{}, err
	}
	epic, err := s.getWorkEpicUnlocked(ctx, epicID)
	if err != nil {
		return PlanMutationResult{}, err
	}
	if operation := epic.Plan.LastOperation; operation != nil && operation.Action == "plan.mutated" && operation.FromRevision == req.ExpectedRevision && operation.Graph != nil && hashPlanGraph(*operation.Graph) == hashPlanGraph(req.Graph) {
		if err := s.auditOnce(ctx, epic.ID, "", operation.Actor, operation.Action, operation); err != nil {
			return PlanMutationResult{}, err
		}
		return PlanMutationResult{Plan: epic.Plan}, nil
	}
	if req.ExpectedRevision != epic.Plan.Revision {
		return PlanMutationResult{Stale: true, Plan: epic.Plan}, nil
	}
	if epic.Plan.State != PlanDraft {
		return PlanMutationResult{}, fmt.Errorf("%w: Plan must be revised before mutation", ErrPlanNotApprovable)
	}
	if err := validateDraft(req.Graph, epic.Plan.Planning); err != nil {
		return PlanMutationResult{}, err
	}
	if s.planning != nil {
		if err := s.ensureAllPlanningSessions(ctx, &epic); err != nil {
			return PlanMutationResult{}, err
		}
	}
	operation := &PlanOperation{Action: "plan.mutated", FromRevision: epic.Plan.Revision, FromHash: epic.Plan.Hash, Actor: "planner", Graph: &req.Graph}
	if err := s.beginPlanOperation(ctx, &epic, "plan.mutation.requested", operation); err != nil {
		return PlanMutationResult{}, err
	}
	if err := s.applyPlanMutation(ctx, &epic, operation); err != nil {
		return PlanMutationResult{}, s.recoveryFailure(err)
	}
	return PlanMutationResult{Plan: epic.Plan}, nil
}

func (s *Service) AddPlanningWork(ctx context.Context, epicID string, req AddPlanningWorkRequest) (PlanMutationResult, error) {
	s.pourMu.Lock()
	defer s.pourMu.Unlock()
	if err := s.requireMutationStore(ctx); err != nil {
		return PlanMutationResult{}, err
	}
	epic, err := s.getWorkEpicUnlocked(ctx, epicID)
	if err != nil {
		return PlanMutationResult{}, err
	}
	if operation := epic.Plan.LastOperation; operation != nil && operation.Action == "planning.added" && operation.FromRevision == req.ExpectedRevision && samePlanningTargetIdentity(operation.Target, req.Target) {
		if err := s.auditOnce(ctx, epic.ID, operation.WorkID, operation.Actor, operation.Action, operation); err != nil {
			return PlanMutationResult{}, err
		}
		return PlanMutationResult{Plan: epic.Plan}, nil
	}
	if req.ExpectedRevision != epic.Plan.Revision {
		return PlanMutationResult{Stale: true, Plan: epic.Plan}, nil
	}
	if !stableID.MatchString(req.Target.ID) || req.Target.HostID != localHostID {
		return PlanMutationResult{}, errors.New("planning work requires a local target")
	}
	if !req.AcknowledgeLocalExecution {
		return PlanMutationResult{}, errors.New("local non-isolated execution must be acknowledged")
	}
	repository, err := filepath.EvalSymlinks(req.Target.Repository)
	if err != nil {
		return PlanMutationResult{}, errors.New("planning work repository must be an existing local directory")
	}
	req.Target.Repository = filepath.Clean(repository)
	if epic.Plan.State != PlanDraft {
		return PlanMutationResult{}, fmt.Errorf("%w: Plan must be revised before adding Planning Work", ErrPlanNotApprovable)
	}
	for _, target := range epic.Plan.Draft.Targets {
		if target.ID == req.Target.ID || target.Repository == req.Target.Repository {
			return PlanMutationResult{}, errors.New("planning work target already exists")
		}
	}
	if s.planning != nil {
		if err := s.ensureAllPlanningSessions(ctx, &epic); err != nil {
			return PlanMutationResult{}, err
		}
	}
	if err := s.acks.UpsertFactoryLocalExecutionAck(ctx, localHostID, req.Target.Repository, planningProfileID, planningProfileVersion, operatorActor, time.Now()); err != nil {
		return PlanMutationResult{}, fmt.Errorf("%w: record local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}
	operation := &PlanOperation{Action: "planning.added", FromRevision: epic.Plan.Revision, FromHash: epic.Plan.Hash, Actor: operatorActor, Target: &req.Target}
	if err := s.beginPlanOperation(ctx, &epic, "planning.addition.requested", operation); err != nil {
		return PlanMutationResult{}, err
	}
	workID, err := s.createPlanningWork(ctx, epic, req.Target)
	if err != nil {
		return PlanMutationResult{}, s.recoveryFailure(err)
	}
	operation.WorkID = workID
	if err := s.applyPlanningAddition(ctx, &epic, operation); err != nil {
		return PlanMutationResult{}, s.recoveryFailure(err)
	}
	return PlanMutationResult{Plan: epic.Plan}, nil
}

func samePlanningTargetIdentity(stored *PlanTarget, requested PlanTarget) bool {
	if stored == nil {
		return false
	}
	requested.Repository = filepath.Clean(requested.Repository)
	return *stored == requested
}

func (s *Service) createPlanningWork(ctx context.Context, epic WorkEpic, target PlanTarget) (string, error) {
	metadata := map[string]string{
		"ocman.contract":           "1",
		"ocman.kind":               "agent-work",
		"ocman.formula_id":         epic.FormulaID,
		"ocman.formula_version":    fmt.Sprint(epic.FormulaVersion),
		"ocman.formula_revision":   fmt.Sprint(epic.FormulaRevision),
		"ocman.formula_hash":       epic.FormulaHash,
		"ocman.formula_origin":     string(epic.FormulaOrigin),
		"ocman.instantiation_id":   epic.InstantiationID,
		"ocman.work_epic_id":       epic.ID,
		"ocman.permission_profile": planningProfile,
		"ocman.planning_target":    target.ID,
		"ocman.target_repository":  target.Repository,
	}
	path, cleanup, err := writeJSONTemp(s.dir, "planning-metadata-*.json", metadata)
	if err != nil {
		return "", err
	}
	defer cleanup()
	beadsDir := filepath.Join(s.dir, "beads")
	bd, err := s.runner.LookPath("bd")
	if err != nil {
		return "", fmt.Errorf("%w: Beads is unavailable", ErrFactoryUnavailable)
	}
	out, err := run(ctx, s.runner, bd, s.dir, []string{"create", "Plan: " + target.ID, "--type", "task", "--parent", epic.ID, "--metadata", "@" + path, "--json"}, beadsCommandEnv(beadsDir))
	if err != nil {
		return "", fmt.Errorf("%w: create Planning Work: %w", ErrBeadsFailure, err)
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if !decodeOne(out, &envelope) || envelope.SchemaVersion != 1 || envelope.Data.ID == "" {
		return "", fmt.Errorf("%w: unsupported Planning Work response", ErrBeadsFailure)
	}
	return envelope.Data.ID, nil
}

func (s *Service) beginPlanOperation(ctx context.Context, epic *WorkEpic, requestedAction string, operation *PlanOperation) error {
	if err := s.auditOnce(ctx, epic.ID, operation.WorkID, operation.Actor, requestedAction, operation); err != nil {
		return err
	}
	epic.Plan.PendingOperation = operation
	return s.persistPlan(ctx, epic)
}

func (s *Service) finishPlanOperation(ctx context.Context, epic *WorkEpic, operation *PlanOperation) error {
	epic.Plan.PendingOperation = nil
	epic.Plan.LastOperation = operation
	if err := s.persistPlan(ctx, epic); err != nil {
		return err
	}
	return s.auditOnce(ctx, epic.ID, operation.WorkID, operation.Actor, operation.Action, operation)
}

func (s *Service) applyPlanMutation(ctx context.Context, epic *WorkEpic, operation *PlanOperation) error {
	if operation.Graph == nil || epic.Plan.Revision != operation.FromRevision || epic.Plan.Hash != operation.FromHash {
		return errors.New("pending Plan mutation no longer matches its source revision")
	}
	epic.Plan.Revision++
	epic.Plan.State = PlanDraft
	epic.Plan.Draft = *operation.Graph
	epic.Plan.Hash = hashPlanGraph(epic.Plan.Draft)
	epic.Plan.Approval = nil
	epic.Plan.Validation = validateComplete(epic.Plan)
	return s.finishPlanOperation(ctx, epic, operation)
}

func (s *Service) applyPlanningAddition(ctx context.Context, epic *WorkEpic, operation *PlanOperation) error {
	if operation.Target == nil || operation.WorkID == "" || epic.Plan.Revision != operation.FromRevision || epic.Plan.Hash != operation.FromHash {
		return errors.New("pending Planning Work addition no longer matches its source revision")
	}
	epic.Plan.Revision++
	epic.Plan.State = PlanDraft
	epic.Plan.Approval = nil
	epic.Plan.Draft.Targets = append(epic.Plan.Draft.Targets, *operation.Target)
	epic.Plan.Planning = append(epic.Plan.Planning, PlanningWork{ID: operation.WorkID, TargetID: operation.Target.ID, Repository: operation.Target.Repository, Status: "open"})
	epic.Plan.Hash = hashPlanGraph(epic.Plan.Draft)
	epic.Plan.Validation = validateComplete(epic.Plan)
	session, err := s.ensurePlanningSession(ctx, *epic, epic.Plan.Planning[len(epic.Plan.Planning)-1])
	if err != nil {
		return err
	}
	epic.Plan.Planning[len(epic.Plan.Planning)-1].Session = session
	return s.finishPlanOperation(ctx, epic, operation)
}

func (s *Service) recoveryFailure(err error) error {
	s.setRecoveryErr(err)
	return err
}

func (s *Service) ApprovePlan(ctx context.Context, epicID string, req PlanDecisionRequest) (Plan, error) {
	s.pourMu.Lock()
	defer s.pourMu.Unlock()
	if err := s.requireMutationStore(ctx); err != nil {
		return Plan{}, err
	}
	epic, err := s.getWorkEpicUnlocked(ctx, epicID)
	if err != nil {
		return Plan{}, err
	}
	if req.ExpectedRevision != epic.Plan.Revision || req.ExpectedHash == "" || req.ExpectedHash != epic.Plan.Hash {
		return Plan{}, &PlanConflictError{Current: epic.Plan}
	}
	if epic.Plan.State == PlanApproved && epic.Plan.Approval != nil && epic.Plan.Approval.Revision == req.ExpectedRevision && epic.Plan.Approval.Hash == req.ExpectedHash {
		if err := s.runIssueCommand(ctx, "close", epic.Planning.ApprovalGateID, "--reason", approvalReason(epic.Plan.Approval.Revision, epic.Plan.Approval.Hash, epic.Plan.Approval.Reason), "--json"); err != nil {
			return Plan{}, err
		}
		if err := s.auditOnce(ctx, epic.ID, "", epic.Plan.Approval.Actor, "plan.approved", epic.Plan.Approval); err != nil {
			return Plan{}, err
		}
		if s.attempts != nil {
			if err := s.runDispatcher(ctx); err != nil {
				return Plan{}, s.recoveryFailure(err)
			}
		}
		return epic.Plan, nil
	}
	if epic.Plan.State != PlanDraft || hashPlanGraph(epic.Plan.Draft) != epic.Plan.Hash {
		return Plan{}, fmt.Errorf("%w: draft state or hash is invalid", ErrPlanNotApprovable)
	}
	if err := validateDraft(epic.Plan.Draft, epic.Plan.Planning); err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrPlanNotApprovable, err)
	}
	epic.Plan.Validation = validateComplete(epic.Plan)
	if len(epic.Plan.Validation) != 0 {
		return Plan{}, fmt.Errorf("%w: %s", ErrPlanNotApprovable, strings.Join(epic.Plan.Validation, "; "))
	}
	if !req.AcknowledgeLocalExecution {
		return Plan{}, fmt.Errorf("%w: local non-isolated execution must be acknowledged", ErrPlanNotApprovable)
	}
	if err := s.auditOnce(ctx, epic.ID, "", reqActor(req.Actor), "plan.approval.requested", map[string]any{"revision": epic.Plan.Revision, "hash": epic.Plan.Hash, "reason": req.Reason}); err != nil {
		return Plan{}, err
	}
	targets := map[string]PlanTarget{}
	for _, target := range epic.Plan.Draft.Targets {
		targets[target.ID] = target
	}
	acknowledged := map[string]bool{}
	for _, item := range epic.Plan.Draft.Items {
		if item.Kind != "agent-work" || acknowledged[item.TargetID+"\x00"+item.Profile] {
			continue
		}
		profile := strings.SplitN(item.Profile, "/", 2)
		if err := s.acks.UpsertFactoryLocalExecutionAck(ctx, localHostID, targets[item.TargetID].Repository, profile[0], profile[1], reqActor(req.Actor), time.Now()); err != nil {
			return Plan{}, fmt.Errorf("%w: record local execution acknowledgement: %w", ErrFactoryUnavailable, err)
		}
		acknowledged[item.TargetID+"\x00"+item.Profile] = true
	}
	frozen := cloneGraph(epic.Plan.Draft)
	epic.Plan.State = PlanApproved
	epic.Plan.Approval = &PlanApproval{
		Revision: epic.Plan.Revision, Hash: epic.Plan.Hash, Actor: reqActor(req.Actor), ApprovedAt: time.Now().UTC(),
		FormulaID: epic.FormulaID, FormulaVersion: epic.FormulaVersion, FormulaHash: epic.FormulaHash, FormulaOrigin: string(epic.FormulaOrigin), InstantiationID: epic.InstantiationID, Reason: strings.TrimSpace(req.Reason), Graph: frozen,
	}
	if err := s.persistPlan(ctx, &epic); err != nil {
		return Plan{}, err
	}
	if err := s.runIssueCommand(ctx, "close", epic.Planning.ApprovalGateID, "--reason", approvalReason(epic.Plan.Revision, epic.Plan.Hash, req.Reason), "--json"); err != nil {
		return Plan{}, err
	}
	if err := s.auditOnce(ctx, epic.ID, "", reqActor(req.Actor), "plan.approved", epic.Plan.Approval); err != nil {
		return Plan{}, err
	}
	if s.attempts != nil {
		if err := s.runDispatcher(ctx); err != nil {
			return Plan{}, s.recoveryFailure(err)
		}
	}
	return epic.Plan, nil
}

func approvalReason(revision int, hash, reason string) string {
	message := fmt.Sprintf("approved Plan revision %d hash %s", revision, hash)
	if strings.TrimSpace(reason) != "" {
		message += ": " + strings.TrimSpace(reason)
	}
	return message
}

func (s *Service) CompletePlanningWork(ctx context.Context, epicID, workID string, req CompletePlanningWorkRequest) (Plan, error) {
	s.pourMu.Lock()
	defer s.pourMu.Unlock()
	if err := s.requireMutationStore(ctx); err != nil {
		return Plan{}, err
	}
	epic, err := s.getWorkEpicUnlocked(ctx, epicID)
	if err != nil {
		return Plan{}, err
	}
	if operation := epic.Plan.LastOperation; operation != nil && operation.Action == "planning.succeeded" && operation.WorkID == workID && operation.FromRevision == req.ExpectedRevision && operation.FromHash == req.ExpectedHash {
		if err := s.auditOnce(ctx, epic.ID, workID, operation.Actor, operation.Action, operation); err != nil {
			return Plan{}, err
		}
		return epic.Plan, nil
	}
	if req.ExpectedRevision != epic.Plan.Revision || req.ExpectedHash != epic.Plan.Hash {
		return Plan{}, &PlanConflictError{Current: epic.Plan}
	}
	if epic.Plan.State != PlanDraft {
		return Plan{}, fmt.Errorf("%w: Planning Work can only complete on a draft Plan", ErrPlanNotApprovable)
	}
	operation := &PlanOperation{Action: "planning.succeeded", FromRevision: epic.Plan.Revision, FromHash: epic.Plan.Hash, Actor: reqActor(req.Actor), WorkID: workID}
	if err := s.beginPlanOperation(ctx, &epic, "planning.completion.requested", operation); err != nil {
		return Plan{}, err
	}
	if err := s.applyPlanningCompletion(ctx, &epic, operation); err != nil {
		return Plan{}, s.recoveryFailure(err)
	}
	return epic.Plan, nil
}

func (s *Service) applyPlanningCompletion(ctx context.Context, epic *WorkEpic, operation *PlanOperation) error {
	found := false
	for i := range epic.Plan.Planning {
		if epic.Plan.Planning[i].ID == operation.WorkID {
			epic.Plan.Planning[i].Status = "closed"
			epic.Plan.Planning[i].Outcome = "succeeded"
			epic.Plan.Planning[i].CompletedRevision = operation.FromRevision
			epic.Plan.Planning[i].CompletedHash = operation.FromHash
			found = true
			if epic.Plan.Planning[i].metadata == nil {
				return errors.New("planning work metadata is unavailable")
			}
			epic.Plan.Planning[i].metadata["ocman.terminal_outcome"] = "succeeded"
			epic.Plan.Planning[i].metadata["ocman.plan_revision"] = strconv.Itoa(operation.FromRevision)
			epic.Plan.Planning[i].metadata["ocman.plan_hash"] = operation.FromHash
			if err := s.persistIssueMetadata(ctx, operation.WorkID, epic.Plan.Planning[i].metadata); err != nil {
				return err
			}
		}
	}
	if !found {
		return errors.New("planning work does not belong to this Work Epic")
	}
	if err := s.runIssueCommand(ctx, "close", operation.WorkID, "--reason", "Planning Work succeeded", "--json"); err != nil {
		return err
	}
	epic.Plan.Validation = validateComplete(epic.Plan)
	if err := s.finishPlanOperation(ctx, epic, operation); err != nil {
		return err
	}
	return nil
}

func (s *Service) RevisePlan(ctx context.Context, epicID string, req PlanDecisionRequest) (Plan, error) {
	return s.decidePlan(ctx, epicID, req, PlanDraft, "plan.revised", true)
}

func (s *Service) RejectPlan(ctx context.Context, epicID string, req PlanDecisionRequest) (Plan, error) {
	return s.decidePlan(ctx, epicID, req, PlanRejected, "plan.rejected", false)
}

func (s *Service) CancelPlan(ctx context.Context, epicID string, req PlanDecisionRequest) (Plan, error) {
	return s.decidePlan(ctx, epicID, req, PlanCancelled, "plan.cancelled", false)
}

func (s *Service) decidePlan(ctx context.Context, epicID string, req PlanDecisionRequest, state PlanState, action string, reopen bool) (Plan, error) {
	s.pourMu.Lock()
	defer s.pourMu.Unlock()
	if err := s.requireMutationStore(ctx); err != nil {
		return Plan{}, err
	}
	epic, err := s.getWorkEpicUnlocked(ctx, epicID)
	if err != nil {
		return Plan{}, err
	}
	if epic.Plan.LastDecision != nil && epic.Plan.LastDecision.Action == action && epic.Plan.LastDecision.FromRevision == req.ExpectedRevision && epic.Plan.LastDecision.Hash == req.ExpectedHash {
		reason := decisionReason(state, epic.Plan.LastDecision.Reason)
		if state == PlanCancelled {
			if err := s.stopPlanningSessions(ctx, epic); err != nil {
				return Plan{}, err
			}
		}
		if err := s.runIssueCommand(ctx, decisionCommand(state, reopen), epic.Planning.ApprovalGateID, "--reason", reason, "--json"); err != nil {
			return Plan{}, err
		}
		if state == PlanCancelled {
			if err := s.runIssueCommand(ctx, "close", epic.ID, "--force", "--reason", reason, "--json"); err != nil {
				return Plan{}, err
			}
		}
		if err := s.auditOnce(ctx, epic.ID, "", epic.Plan.LastDecision.Actor, action, epic.Plan.LastDecision); err != nil {
			return Plan{}, err
		}
		return epic.Plan, nil
	}
	if req.ExpectedRevision != epic.Plan.Revision || req.ExpectedHash != epic.Plan.Hash {
		return Plan{}, &PlanConflictError{Current: epic.Plan}
	}
	if state == PlanDraft && epic.Plan.State == PlanCancelled {
		return Plan{}, fmt.Errorf("%w: cancelled Plans cannot be revised", ErrPlanNotApprovable)
	}
	if state == PlanRejected && epic.Plan.State != PlanDraft {
		return Plan{}, fmt.Errorf("%w: only draft Plans can be rejected", ErrPlanNotApprovable)
	}
	decision := &PlanDecision{Action: action, FromRevision: epic.Plan.Revision, Revision: epic.Plan.Revision, Hash: epic.Plan.Hash, Actor: reqActor(req.Actor), Reason: strings.TrimSpace(req.Reason)}
	if reopen {
		decision.Revision++
	}
	if err := s.auditOnce(ctx, epic.ID, "", decision.Actor, decisionRequestAction(action), decision); err != nil {
		return Plan{}, err
	}
	if state == PlanCancelled {
		if err := s.stopPlanningSessions(ctx, epic); err != nil {
			return Plan{}, err
		}
	}
	epic.Plan.State = state
	epic.Plan.LastDecision = decision
	if reopen {
		epic.Plan.Revision++
		epic.Plan.Approval = nil
		epic.Plan.Hash = hashPlanGraph(epic.Plan.Draft)
	}
	command := decisionCommand(state, reopen)
	reason := decisionReason(state, req.Reason)
	args := []string{"--reason", reason, "--json"}
	if reopen {
		if err := s.runIssueCommand(ctx, command, epic.Planning.ApprovalGateID, args...); err != nil {
			return Plan{}, err
		}
	}
	if err := s.persistPlan(ctx, &epic); err != nil {
		return Plan{}, err
	}
	if !reopen {
		if err := s.runIssueCommand(ctx, command, epic.Planning.ApprovalGateID, args...); err != nil {
			return Plan{}, err
		}
	}
	if state == PlanCancelled {
		if err := s.runIssueCommand(ctx, "close", epic.ID, "--force", "--reason", reason, "--json"); err != nil {
			return Plan{}, err
		}
	}
	if err := s.auditOnce(ctx, epic.ID, "", decision.Actor, action, decision); err != nil {
		return Plan{}, err
	}
	return epic.Plan, nil
}

func decisionCommand(_ PlanState, reopen bool) string {
	if reopen {
		return "reopen"
	}
	return "close"
}

func (s *Service) stopPlanningSessions(ctx context.Context, epic WorkEpic) error {
	if s.store == nil {
		return nil
	}
	for _, work := range epic.Plan.Planning {
		session, ok, err := s.store.GetFactoryPlanningSession(ctx, work.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if s.planning == nil {
			return fmt.Errorf("%w: planning session service is unavailable", ErrFactoryUnavailable)
		}
		if err := s.planning.StopPlanningSession(ctx, session); err != nil {
			return fmt.Errorf("dispose Planning Session: %w", err)
		}
		if err := s.store.DeleteFactoryPlanningSession(ctx, work.ID); err != nil {
			return err
		}
	}
	return nil
}

func decisionReason(state PlanState, reason string) string {
	var message string
	switch state {
	case PlanRejected:
		message = "Plan rejected"
	case PlanCancelled:
		message = "Plan cancelled"
	default:
		message = "Plan revised"
	}
	if strings.TrimSpace(reason) != "" {
		message += ": " + strings.TrimSpace(reason)
	}
	return message
}

func decisionRequestAction(action string) string {
	switch action {
	case "plan.revised":
		return "plan.revision.requested"
	case "plan.rejected":
		return "plan.rejection.requested"
	default:
		return "plan.cancellation.requested"
	}
}

func (s *Service) runIssueCommand(ctx context.Context, command, id string, args ...string) error {
	bd, err := s.runner.LookPath("bd")
	if err != nil {
		return fmt.Errorf("%w: Beads is unavailable", ErrFactoryUnavailable)
	}
	commandArgs := append([]string{command, id}, args...)
	_, err = run(ctx, s.runner, bd, s.dir, commandArgs, beadsCommandEnv(filepath.Join(s.dir, "beads")))
	if err != nil {
		return fmt.Errorf("%w: %s issue: %w", ErrBeadsFailure, command, err)
	}
	return nil
}

func cloneGraph(graph PlanGraph) PlanGraph {
	encoded, _ := json.Marshal(graph)
	var clone PlanGraph
	_ = json.Unmarshal(encoded, &clone)
	return clone
}

func validateDraft(graph PlanGraph, planning []PlanningWork) error {
	if strings.TrimSpace(graph.Intent) == "" {
		return errors.New("plan intent is required")
	}
	targets := map[string]bool{}
	planningTargets := map[string]string{}
	for _, work := range planning {
		if _, exists := planningTargets[work.TargetID]; exists {
			return errors.New("each target must have exactly one Planning Work")
		}
		planningTargets[work.TargetID] = work.Repository
	}
	for _, target := range graph.Targets {
		if !stableID.MatchString(target.ID) || targets[target.ID] {
			return errors.New("plan target IDs must be non-empty and unique")
		}
		canonical, err := filepath.EvalSymlinks(target.Repository)
		if err != nil || target.HostID != localHostID || !filepath.IsAbs(target.Repository) || filepath.Clean(canonical) != target.Repository {
			return errors.New("plan targets must be canonical repositories on the local host")
		}
		if planningTargets[target.ID] != target.Repository {
			return fmt.Errorf("target %q has no repository-scoped Planning Work", target.ID)
		}
		targets[target.ID] = true
	}
	items := map[string]bool{}
	for _, item := range graph.Items {
		if !stableID.MatchString(item.ID) || items[item.ID] {
			return errors.New("plan item IDs must be non-empty and unique")
		}
		if !validItem(item, targets) {
			return fmt.Errorf("invalid plan item %q", item.ID)
		}
		items[item.ID] = true
	}
	for _, edge := range graph.Dependencies {
		if edge.From == edge.To || !items[edge.From] || !items[edge.To] {
			return errors.New("plan dependencies must reference distinct items")
		}
	}
	if cyclic(graph.Dependencies) {
		return errors.New("plan dependencies must be acyclic")
	}
	return nil
}

func validItem(item PlanItem, targets map[string]bool) bool {
	if strings.TrimSpace(item.Title) == "" {
		return false
	}
	switch item.Kind {
	case "agent-work":
		return targets[item.TargetID] && (item.Profile == "factory-implement/v1" || item.Profile == "factory-review/v1")
	case "system-work":
		return targets[item.TargetID] && item.Profile == ""
	case "gate":
		return targets[item.TargetID] && item.Profile == "" && (item.GateType == "provider-check" || item.GateType == "human-merge")
	case "delivery":
		return targets[item.TargetID] && item.Profile == "factory-deliver/v1"
	default:
		return false
	}
}

func cyclic(edges []PlanDependency) bool {
	next := map[string][]string{}
	for _, edge := range edges {
		next[edge.From] = append(next[edge.From], edge.To)
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, child := range next[id] {
			if visit(child) {
				return true
			}
		}
		delete(visiting, id)
		visited[id] = true
		return false
	}
	for id := range next {
		if visit(id) {
			return true
		}
	}
	return false
}

func validateComplete(plan Plan) []string {
	problems := []string{}
	planningTargets := map[string]int{}
	for _, work := range plan.Planning {
		planningTargets[work.TargetID]++
		if work.Status != "closed" || work.Outcome != "succeeded" || work.CompletedRevision != plan.Revision || work.CompletedHash != plan.Hash {
			problems = append(problems, "Planning Work "+work.ID+" has not succeeded")
		}
	}
	deliveries := map[string]int{}
	deliveryIDs, providerCheckIDs := map[string]string{}, map[string]string{}
	providerChecks, humanMerges := map[string]int{}, map[string]int{}
	for _, item := range plan.Draft.Items {
		if item.Kind == "delivery" {
			deliveries[item.TargetID]++
			deliveryIDs[item.TargetID] = item.ID
		}
		if item.Kind == "gate" && item.GateType == "provider-check" {
			providerChecks[item.TargetID]++
			providerCheckIDs[item.TargetID] = item.ID
		}
		if item.Kind == "gate" && item.GateType == "human-merge" {
			humanMerges[item.TargetID]++
		}
	}
	edges := map[string]bool{}
	for _, edge := range plan.Draft.Dependencies {
		edges[edge.From+"\x00"+edge.To] = true
	}
	for _, target := range plan.Draft.Targets {
		if planningTargets[target.ID] != 1 {
			problems = append(problems, "target "+target.ID+" must have one Planning Work")
		}
		if deliveries[target.ID] != 1 {
			problems = append(problems, "target "+target.ID+" must have one Delivery")
		}
		if providerChecks[target.ID] != 1 {
			problems = append(problems, "target "+target.ID+" must have one provider-check Gate")
		}
		if humanMerges[target.ID] != 1 {
			problems = append(problems, "target "+target.ID+" must have one human-merge Gate")
		}
		if providerChecks[target.ID] == 1 && deliveries[target.ID] == 1 && !edges[providerCheckIDs[target.ID]+"\x00"+deliveryIDs[target.ID]] {
			problems = append(problems, "target "+target.ID+" provider-check Gate must depend on Delivery")
		}
		if humanMerges[target.ID] == 1 && providerChecks[target.ID] == 1 {
			for _, item := range plan.Draft.Items {
				if item.Kind == "gate" && item.GateType == "human-merge" && item.TargetID == target.ID && !edges[item.ID+"\x00"+providerCheckIDs[target.ID]] {
					problems = append(problems, "target "+target.ID+" human-merge Gate must depend on provider-check Gate")
				}
			}
		}
		if target.DeliveryBase.Remote == "" || target.DeliveryBase.BaseBranch == "" || target.DeliveryBase.BaseSHA == "" {
			problems = append(problems, "target "+target.ID+" has no immutable Delivery base")
		}
	}
	if len(plan.Draft.Targets) == 0 {
		problems = append(problems, "plan has no targets")
	}
	if len(plan.Draft.Items) == 0 {
		problems = append(problems, "plan has no work items")
	}
	sort.Strings(problems)
	return problems
}

func (s *Service) requireMutationStore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	owned := s.owned
	if !owned {
		return fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	if s.store == nil {
		return fmt.Errorf("%w: Factory audit store is unavailable", ErrFactoryUnavailable)
	}
	if err := s.cleanupPlanningSessions(ctx); err != nil {
		s.setRecoveryErr(err)
		return fmt.Errorf("%w: Factory cleanup has not succeeded: %w", ErrFactoryUnavailable, err)
	}
	if s.getRecoveryErr() != nil {
		if err := s.recoverPlanningSessions(ctx); err != nil {
			s.setRecoveryErr(err)
			return fmt.Errorf("%w: Factory recovery has not succeeded: %w", ErrFactoryUnavailable, err)
		}
		if s.attempts != nil {
			if err := s.runDispatcher(ctx); err != nil {
				s.setRecoveryErr(err)
				return fmt.Errorf("%w: Factory recovery has not succeeded: %w", ErrFactoryUnavailable, err)
			}
		}
		s.setRecoveryErr(nil)
	}
	return nil
}

func (s *Service) getWorkEpicUnlocked(ctx context.Context, id string) (WorkEpic, error) {
	beadsDir := filepath.Join(s.dir, "beads")
	path, _, failure := compatibleBeads(ctx, beadsDir, s.runner)
	if failure.Reason != "" {
		return WorkEpic{}, beadsError(failure)
	}
	epics, err := listWorkEpics(ctx, path, beadsDir, s.runner)
	if err != nil {
		return WorkEpic{}, err
	}
	for _, epic := range epics {
		if epic.ID == id {
			if epic.PlanError != "" {
				return WorkEpic{}, fmt.Errorf("%w: %s", ErrPlanIncompatible, epic.PlanError)
			}
			s.decoratePlanningSessions(ctx, &epic)
			return epic, nil
		}
	}
	return WorkEpic{}, ErrWorkEpicNotFound
}

func (s *Service) persistPlan(ctx context.Context, epic *WorkEpic) error {
	if epic.metadata == nil {
		return errors.New("work epic metadata is unavailable")
	}
	encoded, err := json.Marshal(epic.Plan)
	if err != nil {
		return err
	}
	epic.metadata[planMetadataKey] = string(encoded)
	path, cleanup, err := writeJSONTemp(s.dir, "plan-metadata-*.json", epic.metadata)
	if err != nil {
		return err
	}
	defer cleanup()
	beadsDir := filepath.Join(s.dir, "beads")
	bd, err := s.runner.LookPath("bd")
	if err != nil {
		return fmt.Errorf("%w: Beads is unavailable", ErrFactoryUnavailable)
	}
	_, err = run(ctx, s.runner, bd, s.dir, []string{"update", epic.ID, "--metadata", "@" + path, "--json"}, beadsCommandEnv(beadsDir))
	if err != nil {
		return fmt.Errorf("%w: persist plan: %w", ErrBeadsFailure, err)
	}
	return nil
}

func (s *Service) persistIssueMetadata(ctx context.Context, issueID string, metadata map[string]string) error {
	path, cleanup, err := writeJSONTemp(s.dir, "issue-metadata-*.json", metadata)
	if err != nil {
		return err
	}
	defer cleanup()
	bd, err := s.runner.LookPath("bd")
	if err != nil {
		return fmt.Errorf("%w: Beads is unavailable", ErrFactoryUnavailable)
	}
	_, err = run(ctx, s.runner, bd, s.dir, []string{"update", issueID, "--metadata", "@" + path, "--json"}, beadsCommandEnv(filepath.Join(s.dir, "beads")))
	if err != nil {
		return fmt.Errorf("%w: persist issue metadata: %w", ErrBeadsFailure, err)
	}
	return nil
}

func writeJSONTemp(dir, pattern string, value any) (string, func(), error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", func() {}, fmt.Errorf("prepare Factory metadata: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	err = json.NewEncoder(file).Encode(value)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("prepare Factory metadata: %w", err)
	}
	return path, cleanup, nil
}

func (s *Service) decoratePlanningSessions(ctx context.Context, epic *WorkEpic) {
	if s.store == nil {
		return
	}
	for i := range epic.Plan.Planning {
		if session, ok, err := s.store.GetFactoryPlanningSession(ctx, epic.Plan.Planning[i].ID); err == nil && ok {
			epic.Plan.Planning[i].Session = session
		}
	}
}

func (s *Service) ensureAllPlanningSessions(ctx context.Context, epic *WorkEpic) error {
	for i := range epic.Plan.Planning {
		session, err := s.ensurePlanningSession(ctx, *epic, epic.Plan.Planning[i])
		if err != nil {
			return err
		}
		epic.Plan.Planning[i].Session = session
	}
	return nil
}

func (s *Service) recoverPlanningSessions(ctx context.Context) error {
	if err := s.cleanupPlanningSessions(ctx); err != nil {
		return err
	}
	beadsDir := filepath.Join(s.dir, "beads")
	path, _, failure := compatibleBeads(ctx, beadsDir, s.runner)
	if failure.Reason != "" {
		return beadsError(failure)
	}
	epics, err := listWorkEpics(ctx, path, beadsDir, s.runner)
	if err != nil {
		return err
	}
	for i := range epics {
		s.decoratePlanningSessions(ctx, &epics[i])
		if operation := epics[i].Plan.PendingOperation; operation != nil {
			if err := s.reconcilePendingPlanOperation(ctx, &epics[i], operation); err != nil {
				return err
			}
		}
		if operation := epics[i].Plan.LastOperation; operation != nil {
			if operation.Action == "planning.added" {
				if err := s.ensureAllPlanningSessions(ctx, &epics[i]); err != nil {
					return err
				}
			}
			if err := s.auditOnce(ctx, epics[i].ID, operation.WorkID, operation.Actor, operation.Action, operation); err != nil {
				return err
			}
		}
		if epics[i].Plan.State == PlanApproved && epics[i].Plan.Approval != nil {
			approval := epics[i].Plan.Approval
			if err := s.runIssueCommand(ctx, "close", epics[i].Planning.ApprovalGateID, "--reason", approvalReason(approval.Revision, approval.Hash, approval.Reason), "--json"); err != nil {
				return err
			}
			if err := s.auditOnce(ctx, epics[i].ID, "", approval.Actor, "plan.approved", approval); err != nil {
				return err
			}
			continue
		}
		if decision := epics[i].Plan.LastDecision; decision != nil {
			switch epics[i].Plan.State {
			case PlanDraft:
				if decision.Action == "plan.revised" {
					if err := s.runIssueCommand(ctx, "reopen", epics[i].Planning.ApprovalGateID, "--reason", decisionReason(PlanDraft, decision.Reason), "--json"); err != nil {
						return err
					}
				}
			case PlanRejected, PlanCancelled:
				if epics[i].Plan.State == PlanCancelled {
					if err := s.stopPlanningSessions(ctx, epics[i]); err != nil {
						return err
					}
				}
				if err := s.runIssueCommand(ctx, "close", epics[i].Planning.ApprovalGateID, "--reason", decisionReason(epics[i].Plan.State, decision.Reason), "--json"); err != nil {
					return err
				}
				if epics[i].Plan.State == PlanCancelled {
					if err := s.runIssueCommand(ctx, "close", epics[i].ID, "--force", "--reason", decisionReason(PlanCancelled, decision.Reason), "--json"); err != nil {
						return err
					}
				}
			}
			if err := s.auditOnce(ctx, epics[i].ID, "", decision.Actor, decision.Action, decision); err != nil {
				return err
			}
		}
		if epics[i].Plan.State != PlanDraft {
			continue
		}
		if epics[i].metadata[planMetadataKey] == "" {
			if err := s.persistPlan(ctx, &epics[i]); err != nil {
				return err
			}
		}
		if err := s.ensureAllPlanningSessions(ctx, &epics[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) cleanupPlanningSessions(ctx context.Context) error {
	cleanups := s.pendingPlanningSessionCleanups()
	persisted, listErr := s.store.ListFactoryPlanningSessionCleanups(ctx)
	for workID, session := range persisted {
		if _, pending := cleanups[workID]; !pending {
			cleanups[workID] = session
		}
	}
	workIDs := make([]string, 0, len(cleanups))
	for workID := range cleanups {
		workIDs = append(workIDs, workID)
	}
	sort.Strings(workIDs)
	for _, workID := range workIDs {
		if s.planning == nil {
			return fmt.Errorf("%w: planning session service is unavailable", ErrFactoryUnavailable)
		}
		if err := s.planning.StopPlanningSession(ctx, cleanups[workID]); err != nil {
			return errors.Join(listErr, fmt.Errorf("dispose restricted Planning Session: %w", err))
		}
		s.clearPendingPlanningSessionCleanup(workID, cleanups[workID])
		if err := s.store.DeleteFactoryPlanningSessionCleanup(ctx, workID); err != nil {
			return errors.Join(listErr, err)
		}
	}
	return listErr
}

func (s *Service) reconcilePendingPlanOperation(ctx context.Context, epic *WorkEpic, operation *PlanOperation) error {
	switch operation.Action {
	case "plan.mutated":
		return s.applyPlanMutation(ctx, epic, operation)
	case "planning.added":
		if operation.WorkID == "" {
			workID, err := s.findPlanningWork(ctx, epic.ID, operation.Target)
			if err != nil {
				return err
			}
			if workID == "" {
				workID, err = s.createPlanningWork(ctx, *epic, *operation.Target)
				if err != nil {
					return err
				}
			}
			operation.WorkID = workID
		}
		return s.applyPlanningAddition(ctx, epic, operation)
	case "planning.succeeded":
		return s.applyPlanningCompletion(ctx, epic, operation)
	default:
		return fmt.Errorf("unsupported pending Plan operation %q", operation.Action)
	}
}

func (s *Service) findPlanningWork(ctx context.Context, epicID string, target *PlanTarget) (string, error) {
	if target == nil {
		return "", errors.New("pending Planning Work target is unavailable")
	}
	beadsDir := filepath.Join(s.dir, "beads")
	path, _, failure := compatibleBeads(ctx, beadsDir, s.runner)
	if failure.Reason != "" {
		return "", beadsError(failure)
	}
	issues, err := listFactoryIssues(ctx, path, beadsDir, s.runner)
	if err != nil {
		return "", err
	}
	var found string
	for _, issue := range issues {
		if issue.Metadata["ocman.kind"] != "agent-work" || issue.Metadata["ocman.work_epic_id"] != epicID || issue.Metadata["ocman.planning_target"] != target.ID || issue.Metadata["ocman.target_repository"] != target.Repository {
			continue
		}
		if found != "" {
			return "", errors.New("multiple Planning Work items match the pending addition")
		}
		found = issue.ID
	}
	return found, nil
}

func (s *Service) ensurePlanningSession(ctx context.Context, epic WorkEpic, work PlanningWork) (PlanningSession, error) {
	if s.store == nil || s.planning == nil {
		return PlanningSession{}, fmt.Errorf("%w: planning session service is unavailable", ErrFactoryUnavailable)
	}
	if session, ok, err := s.store.GetFactoryPlanningSession(ctx, work.ID); err != nil {
		return PlanningSession{}, err
	} else if ok {
		alive, err := s.planning.ProbePlanningSession(ctx, session)
		if err != nil {
			return PlanningSession{}, fmt.Errorf("probe Planning Session: %w", err)
		}
		if alive {
			return session, nil
		}
		if err := s.store.DeleteFactoryPlanningSession(ctx, work.ID); err != nil {
			return PlanningSession{}, fmt.Errorf("clear dead Planning Session: %w", err)
		}
	}
	session, err := s.planning.LaunchPlanningSession(ctx, PlanningSessionRequest{EpicID: epic.ID, WorkID: work.ID, Repository: work.Repository, Title: "Plan: " + epic.Goal})
	if err != nil {
		if session.ID != "" && session.Platform != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), beadsTimeout)
			defer cancel()
			cleanupErr := s.recordPlanningSessionCleanupFailure(cleanupCtx, epic.ID, work.ID, session, err)
			return PlanningSession{}, errors.Join(fmt.Errorf("launch Planning Session: %w", err), cleanupErr)
		}
		return PlanningSession{}, fmt.Errorf("launch Planning Session: %w", err)
	}
	if session.ID == "" || session.Platform == "" {
		return PlanningSession{}, errors.New("planning session launcher returned no session")
	}
	if err := s.store.PutFactoryPlanningSession(ctx, epic.ID, work.ID, session); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), beadsTimeout)
		defer cancel()
		stopErr := s.planning.StopPlanningSession(cleanupCtx, session)
		var cleanupErr error
		if stopErr != nil {
			cleanupErr = s.recordPlanningSessionCleanupFailure(cleanupCtx, epic.ID, work.ID, session, stopErr)
		}
		clearErr := s.store.DeleteFactoryPlanningSession(cleanupCtx, work.ID)
		return PlanningSession{}, errors.Join(fmt.Errorf("persist Planning Session: %w", err), stopErr, cleanupErr, clearErr)
	}
	return session, nil
}

func (s *Service) recordPlanningSessionCleanupFailure(ctx context.Context, epicID, workID string, session PlanningSession, cause error) error {
	s.retainPlanningSessionCleanupFailure(workID, session, cause)
	if err := s.store.PutFactoryPlanningSessionCleanup(ctx, epicID, workID, session); err != nil {
		s.setRecoveryErr(errors.Join(cause, fmt.Errorf("persist Planning Session cleanup intent: %w", err)))
		return err
	}
	return nil
}

func (s *Service) auditOnce(ctx context.Context, epicID, workID, actor, action string, details any) error {
	return s.store.AppendFactoryAuditOnce(ctx, FactoryAuditRecord{EpicID: epicID, WorkID: workID, Actor: actor, Action: action, Details: details, At: time.Now().UTC()})
}

func reqActor(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return operatorActor
	}
	return strings.TrimSpace(actor)
}
