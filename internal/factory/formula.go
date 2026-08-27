package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultFormulaID       = "ocman/default"
	DefaultFormulaVersion  = 1
	planningProfile        = "factory-plan/v1"
	localHostID            = "local"
	planningProfileID      = "factory-plan"
	planningProfileVersion = "v1"
	operatorActor          = "operator"
)

var (
	ErrWorkEpicNotFound      = errors.New("work epic not found")
	ErrInstantiationConflict = errors.New("factory instantiation conflict")
	ErrFactoryUnavailable    = errors.New("factory unavailable")
	ErrBeadsFailure          = errors.New("beads operation failed")
	stableID                 = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

type Formula struct {
	ID         string             `json:"id"`
	Version    int                `json:"version"`
	Parameters []FormulaParameter `json:"parameters"`
}

type FormulaParameter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func DefaultFormula() Formula {
	return Formula{
		ID:      DefaultFormulaID,
		Version: DefaultFormulaVersion,
		Parameters: []FormulaParameter{
			{Name: "goal", Type: "string"},
			{Name: "initial_project", Type: "local-project"},
		},
	}
}

type CreateWorkEpicRequest struct {
	InstantiationID           string `json:"instantiationId"`
	Goal                      string `json:"goal"`
	InitialProject            string `json:"initialProject"`
	AcknowledgeLocalExecution bool   `json:"acknowledgeLocalExecution"`
}

type PlanningState struct {
	WorkID         string `json:"workId"`
	WorkStatus     string `json:"workStatus"`
	ApprovalGateID string `json:"approvalGateId"`
	ApprovalStatus string `json:"approvalStatus"`
}

type WorkEpic struct {
	ID              string        `json:"id"`
	Status          string        `json:"status"`
	Goal            string        `json:"goal"`
	InitialProject  string        `json:"initialProject"`
	FormulaID       string        `json:"formulaId"`
	FormulaVersion  int           `json:"formulaVersion"`
	InstantiationID string        `json:"instantiationId"`
	Planning        PlanningState `json:"planning"`
}

type graphPlan struct {
	CommitMessage string      `json:"commit_message"`
	Nodes         []graphNode `json:"nodes"`
	Edges         []graphEdge `json:"edges"`
}

type graphNode struct {
	Key          string            `json:"key"`
	Title        string            `json:"title"`
	Type         string            `json:"type"`
	Description  string            `json:"description,omitempty"`
	ParentKey    string            `json:"parent_key,omitempty"`
	Metadata     map[string]string `json:"metadata"`
	MetadataRefs map[string]string `json:"metadata_refs,omitempty"`
}

type graphEdge struct {
	FromKey string `json:"from_key"`
	ToKey   string `json:"to_key"`
	Type    string `json:"type"`
}

type beadsIssue struct {
	ID        string            `json:"id"`
	Status    string            `json:"status"`
	IssueType string            `json:"issue_type"`
	Metadata  map[string]string `json:"metadata"`
}

func (s *Service) CreateWorkEpic(ctx context.Context, req CreateWorkEpicRequest) (WorkEpic, error) {
	if err := validateCreateWorkEpic(req); err != nil {
		return WorkEpic{}, err
	}
	project, err := filepath.EvalSymlinks(req.InitialProject)
	if err != nil {
		return WorkEpic{}, errors.New("initial project must be an existing local directory")
	}
	req.InitialProject = filepath.Clean(project)
	s.mu.RLock()
	owned := s.owned
	s.mu.RUnlock()
	if !owned {
		return WorkEpic{}, fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	if s.acks == nil {
		return WorkEpic{}, fmt.Errorf("%w: acknowledgement store is unavailable", ErrFactoryUnavailable)
	}
	if err := s.acks.UpsertFactoryLocalExecutionAck(ctx, localHostID, req.InitialProject, planningProfileID, planningProfileVersion, operatorActor, time.Now()); err != nil {
		return WorkEpic{}, fmt.Errorf("%w: record local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}

	s.pourMu.Lock()
	defer s.pourMu.Unlock()

	beadsDir := filepath.Join(s.dir, "beads")
	path, _, failure := compatibleBeads(ctx, beadsDir, s.runner)
	if failure.Reason != "" {
		return WorkEpic{}, beadsError(failure)
	}
	epics, err := listWorkEpics(ctx, path, beadsDir, s.runner)
	if err != nil {
		return WorkEpic{}, err
	}
	if epic, err := matchInstantiation(epics, req); epic != nil || err != nil {
		if epic == nil {
			return WorkEpic{}, err
		}
		return *epic, err
	}

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return WorkEpic{}, fmt.Errorf("%w: prepare formula: %w", ErrFactoryUnavailable, err)
	}
	file, err := os.CreateTemp(s.dir, "formula-v1-*.json")
	if err != nil {
		return WorkEpic{}, fmt.Errorf("%w: prepare formula: %w", ErrFactoryUnavailable, err)
	}
	planPath := file.Name()
	defer func() { _ = os.Remove(planPath) }()
	encodeErr := json.NewEncoder(file).Encode(defaultGraph(req))
	closeErr := file.Close()
	if encodeErr != nil {
		return WorkEpic{}, fmt.Errorf("%w: materialize formula: %w", ErrFactoryUnavailable, encodeErr)
	}
	if closeErr != nil {
		return WorkEpic{}, fmt.Errorf("%w: materialize formula: %w", ErrFactoryUnavailable, closeErr)
	}

	out, runErr := run(ctx, s.runner, path, s.dir, []string{"create", "--graph", planPath, "--json"}, beadsCommandEnv(beadsDir))
	ids, parseErr := parseGraphResult(out)
	if runErr == nil && parseErr == nil {
		return WorkEpic{
			ID: ids["epic"], Status: "open", Goal: req.Goal, InitialProject: req.InitialProject,
			FormulaID: DefaultFormulaID, FormulaVersion: DefaultFormulaVersion, InstantiationID: req.InstantiationID,
			Planning: PlanningState{WorkID: ids["planning"], WorkStatus: "open", ApprovalGateID: ids["approval"], ApprovalStatus: "open"},
		}, nil
	}

	// A timeout, process failure, or malformed response may still follow a committed transaction.
	reconcileCtx, cancelReconcile := context.WithTimeout(context.WithoutCancel(ctx), beadsTimeout)
	epics, reconcileErr := listWorkEpics(reconcileCtx, path, beadsDir, s.runner)
	cancelReconcile()
	if reconcileErr == nil {
		if epic, matchErr := matchInstantiation(epics, req); epic != nil || matchErr != nil {
			if epic != nil {
				return *epic, matchErr
			}
			return WorkEpic{}, matchErr
		}
	}
	if runErr != nil {
		return WorkEpic{}, fmt.Errorf("%w: pour default formula: %w", ErrBeadsFailure, runErr)
	}
	return WorkEpic{}, fmt.Errorf("%w: pour default formula: %w", ErrBeadsFailure, parseErr)
}

func (s *Service) ListWorkEpics(ctx context.Context) ([]WorkEpic, error) {
	beadsDir := filepath.Join(s.dir, "beads")
	path, _, failure := compatibleBeads(ctx, beadsDir, s.runner)
	if failure.Reason != "" {
		return nil, beadsError(failure)
	}
	return listWorkEpics(ctx, path, beadsDir, s.runner)
}

func beadsError(failure BeadsHealth) error {
	if failure.Reason == ReasonBeadsCommandFailed {
		return fmt.Errorf("%w: %s", ErrBeadsFailure, failure.Message)
	}
	return fmt.Errorf("%w: %s", ErrFactoryUnavailable, failure.Message)
}

func (s *Service) GetWorkEpic(ctx context.Context, id string) (WorkEpic, error) {
	epics, err := s.ListWorkEpics(ctx)
	if err != nil {
		return WorkEpic{}, err
	}
	for _, epic := range epics {
		if epic.ID == id {
			return epic, nil
		}
	}
	return WorkEpic{}, ErrWorkEpicNotFound
}

func validateCreateWorkEpic(req CreateWorkEpicRequest) error {
	if !req.AcknowledgeLocalExecution {
		return errors.New("local non-isolated execution must be acknowledged")
	}
	if strings.TrimSpace(req.Goal) == "" {
		return errors.New("goal is required")
	}
	if !stableID.MatchString(req.InstantiationID) {
		return errors.New("instantiation ID must be a stable 1-128 character identifier")
	}
	if !filepath.IsAbs(req.InitialProject) || filepath.Clean(req.InitialProject) != req.InitialProject {
		return errors.New("initial project must be an absolute canonical path")
	}
	info, err := os.Stat(req.InitialProject)
	if err != nil || !info.IsDir() {
		return errors.New("initial project must be an existing local directory")
	}
	return nil
}

func defaultGraph(req CreateWorkEpicRequest) graphPlan {
	provenance := func(kind string) map[string]string {
		return map[string]string{
			"ocman.contract":         "1",
			"ocman.kind":             kind,
			"ocman.formula_id":       DefaultFormulaID,
			"ocman.formula_version":  strconv.Itoa(DefaultFormulaVersion),
			"ocman.formula_origin":   "built-in",
			"ocman.instantiation_id": req.InstantiationID,
		}
	}
	epic := provenance("work-epic")
	epic["ocman.goal"] = req.Goal
	epic["ocman.initial_project"] = req.InitialProject
	planning := provenance("agent-work")
	planning["ocman.permission_profile"] = planningProfile
	approval := provenance("gate")
	approval["ocman.gate_type"] = "plan-approval"
	return graphPlan{
		CommitMessage: "ocman factory: instantiate " + req.InstantiationID,
		Nodes: []graphNode{
			{Key: "epic", Title: req.Goal, Type: "epic", Metadata: epic, MetadataRefs: map[string]string{"ocman.planning_work_id": "planning", "ocman.plan_approval_gate_id": "approval"}},
			{Key: "planning", Title: "Plan: " + req.Goal, Type: "task", Description: "Plan delivery for " + req.InitialProject, ParentKey: "epic", Metadata: planning, MetadataRefs: map[string]string{"ocman.work_epic_id": "epic"}},
			{Key: "approval", Title: "Plan approval", Type: "gate", ParentKey: "epic", Metadata: approval, MetadataRefs: map[string]string{"ocman.work_epic_id": "epic"}},
		},
		Edges: []graphEdge{{FromKey: "approval", ToKey: "planning", Type: "blocks"}},
	}
}

func parseGraphResult(data []byte) (map[string]string, error) {
	var out struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			IDs map[string]string `json:"ids"`
		} `json:"data"`
	}
	if !decodeOne(data, &out) || out.SchemaVersion != 1 {
		return nil, errors.New("unsupported Beads graph response")
	}
	for _, key := range []string{"epic", "planning", "approval"} {
		if out.Data.IDs[key] == "" {
			return nil, fmt.Errorf("beads graph response is missing %s", key)
		}
	}
	return out.Data.IDs, nil
}

func listWorkEpics(ctx context.Context, path, beadsDir string, r runner) ([]WorkEpic, error) {
	out, err := run(ctx, r, path, parentDir(beadsDir), []string{
		"--readonly", "list", "--all", "--include-gates", "--limit", "0", "--metadata-field", "ocman.contract=1", "--json",
	}, beadsCommandEnv(beadsDir))
	if err != nil {
		return nil, fmt.Errorf("%w: list Factory work: %w", ErrBeadsFailure, err)
	}
	var envelope struct {
		SchemaVersion int           `json:"schema_version"`
		Data          *[]beadsIssue `json:"data"`
	}
	if !decodeOne(out, &envelope) || envelope.SchemaVersion != 1 || envelope.Data == nil {
		return nil, fmt.Errorf("%w: Beads returned an unsupported Factory list response", ErrBeadsFailure)
	}
	byID := make(map[string]beadsIssue, len(*envelope.Data))
	for _, issue := range *envelope.Data {
		byID[issue.ID] = issue
	}
	var epics []WorkEpic
	for _, issue := range *envelope.Data {
		meta := issue.Metadata
		if issue.IssueType != "epic" || meta["ocman.kind"] != "work-epic" || meta["ocman.formula_id"] != DefaultFormulaID ||
			meta["ocman.formula_version"] != strconv.Itoa(DefaultFormulaVersion) || meta["ocman.formula_origin"] != "built-in" {
			continue
		}
		planning := byID[meta["ocman.planning_work_id"]]
		approval := byID[meta["ocman.plan_approval_gate_id"]]
		if !sameProvenance(planning.Metadata, meta, "agent-work") || !sameProvenance(approval.Metadata, meta, "gate") ||
			planning.Metadata["ocman.work_epic_id"] != issue.ID || planning.Metadata["ocman.permission_profile"] != planningProfile ||
			approval.Metadata["ocman.work_epic_id"] != issue.ID || approval.Metadata["ocman.gate_type"] != "plan-approval" {
			continue
		}
		epics = append(epics, WorkEpic{
			ID: issue.ID, Status: issue.Status, Goal: meta["ocman.goal"], InitialProject: meta["ocman.initial_project"],
			FormulaID: DefaultFormulaID, FormulaVersion: DefaultFormulaVersion, InstantiationID: meta["ocman.instantiation_id"],
			Planning: PlanningState{WorkID: planning.ID, WorkStatus: planning.Status, ApprovalGateID: approval.ID, ApprovalStatus: approval.Status},
		})
	}
	sort.Slice(epics, func(i, j int) bool { return epics[i].ID < epics[j].ID })
	return epics, nil
}

func sameProvenance(child, epic map[string]string, kind string) bool {
	return child["ocman.contract"] == "1" && child["ocman.kind"] == kind &&
		child["ocman.formula_id"] == epic["ocman.formula_id"] &&
		child["ocman.formula_version"] == epic["ocman.formula_version"] &&
		child["ocman.formula_origin"] == epic["ocman.formula_origin"] &&
		child["ocman.instantiation_id"] == epic["ocman.instantiation_id"]
}

func matchInstantiation(epics []WorkEpic, req CreateWorkEpicRequest) (*WorkEpic, error) {
	var found *WorkEpic
	for i := range epics {
		if epics[i].InstantiationID != req.InstantiationID || epics[i].FormulaID != DefaultFormulaID || epics[i].FormulaVersion != DefaultFormulaVersion {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: multiple work epics have instantiation ID %q", ErrInstantiationConflict, req.InstantiationID)
		}
		found = &epics[i]
	}
	if found != nil && (found.Goal != req.Goal || found.InitialProject != req.InitialProject) {
		return nil, fmt.Errorf("%w: instantiation ID %q already belongs to different inputs", ErrInstantiationConflict, req.InstantiationID)
	}
	return found, nil
}
