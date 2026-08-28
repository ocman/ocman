package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

type dispatcherRunner struct {
	mu               sync.Mutex
	issues           map[string]beadsIssue
	nextID           int
	claimFailures    map[string]bool
	claimErrors      map[string]bool
	createErrors     bool
	dependencyErrors bool
	commands         []string
	onClaim          func(string, string) error
	onTerminalWrite  func(string, string) error
	onClose          func(string) error
}

func newDispatcherRunner(issues ...beadsIssue) *dispatcherRunner {
	r := &dispatcherRunner{issues: map[string]beadsIssue{}, claimFailures: map[string]bool{}, claimErrors: map[string]bool{}}
	for _, issue := range issues {
		r.issues[issue.ID] = cloneDispatcherIssue(issue)
	}
	return r
}

func marshalIssueEnvelope(issue beadsIssue) []byte {
	data, _ := json.Marshal(struct {
		SchemaVersion int        `json:"schema_version"`
		Data          beadsIssue `json:"data"`
	}{SchemaVersion: 1, Data: issue})
	return data
}

func (r *dispatcherRunner) LookPath(string) (string, error) { return "/usr/bin/bd", nil }

func (r *dispatcherRunner) Run(ctx context.Context, _ string, _ string, args, _ []string) ([]byte, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	switch {
	case reflect.DeepEqual(args, []string{"version", "--json"}):
		return []byte(versionEnvelope), nil, nil
	case reflect.DeepEqual(args, []string{"--readonly", "status", "--no-activity", "--json"}):
		return []byte(`{"schema_version":1,"data":{"summary":{"total_issues":1}}}`), nil, nil
	case len(args) > 0 && args[0] == "init":
		return []byte(`{"schema_version":1,"data":{}}`), nil, nil
	case len(args) > 1 && args[0] == "--readonly" && args[1] == "list":
		return r.list(false), nil, nil
	case len(args) > 1 && args[0] == "--readonly" && args[1] == "ready":
		return r.list(true), nil, nil
	case len(args) > 0 && args[0] == "create":
		return r.create(args)
	case len(args) > 2 && args[0] == "dep" && args[1] == "add":
		return r.addDependency(args[2], args[3])
	case len(args) > 2 && args[0] == "update" && hasDispatcherArg(args, "--claim"):
		return r.claim(args[1], dispatcherMetadataValue(args, "ocman.attempt_id"))
	case len(args) > 2 && args[0] == "update":
		return r.update(args[1], args)
	case len(args) > 1 && args[0] == "close":
		return r.close(args[1])
	default:
		return nil, nil, fmt.Errorf("unexpected bd command: %v", args)
	}
}

func (r *dispatcherRunner) list(readyOnly bool) []byte {
	issues := make([]beadsIssue, 0, len(r.issues))
	for _, issue := range r.issues {
		if readyOnly && !r.ready(issue) {
			continue
		}
		issues = append(issues, cloneDispatcherIssue(issue))
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	out, _ := json.Marshal(struct {
		SchemaVersion int          `json:"schema_version"`
		Data          []beadsIssue `json:"data"`
	}{1, issues})
	return out
}

func (r *dispatcherRunner) ready(issue beadsIssue) bool {
	if issue.Status != "open" || issue.Metadata["ocman.kind"] != "agent-work" || issue.Assignee != "" {
		return false
	}
	for _, dependency := range issue.Dependencies {
		if dependency.Type == "blocks" && r.issues[dependency.DependsOnID].Status != "closed" {
			return false
		}
	}
	return true
}

func (r *dispatcherRunner) create(args []string) ([]byte, []byte, error) {
	metadataPath := strings.TrimPrefix(dispatcherArgAfter(args, "--metadata"), "@")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, nil, err
	}
	metadata := map[string]string{}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, nil, err
	}
	r.nextID++
	id := fmt.Sprintf("materialized-%d", r.nextID)
	priority := 0
	_, _ = fmt.Sscan(dispatcherArgAfter(args, "--priority"), &priority)
	issue := beadsIssue{ID: id, Title: args[1], Status: "open", Priority: priority, IssueType: dispatcherArgAfter(args, "--type"), CreatedAt: time.Unix(int64(r.nextID), 0).UTC(), Metadata: metadata}
	r.issues[id] = issue
	r.commands = append(r.commands, "create:"+id)
	if r.createErrors {
		return nil, nil, errors.New("create response lost")
	}
	return marshalIssueEnvelope(issue), nil, nil
}

func (r *dispatcherRunner) addDependency(from, to string) ([]byte, []byte, error) {
	issue := r.issues[from]
	issue.Dependencies = append(issue.Dependencies, struct {
		IssueID     string `json:"issue_id"`
		DependsOnID string `json:"depends_on_id"`
		Type        string `json:"type"`
	}{from, to, "blocks"})
	r.issues[from] = issue
	r.commands = append(r.commands, "dep:"+from+":"+to)
	if r.dependencyErrors {
		return nil, nil, errors.New("dependency response lost")
	}
	return []byte(`{"schema_version":1,"data":{}}`), nil, nil
}

func (r *dispatcherRunner) claim(workID, attemptID string) ([]byte, []byte, error) {
	issue := r.issues[workID]
	r.commands = append(r.commands, "claim:"+workID)
	if r.claimFailures[workID] {
		return marshalIssueEnvelope(issue), nil, nil
	}
	if r.onClaim != nil {
		if err := r.onClaim(workID, attemptID); err != nil {
			return nil, nil, err
		}
	}
	issue.Status = "in_progress"
	issue.Metadata["ocman.attempt_id"] = attemptID
	r.issues[workID] = issue
	if r.claimErrors[workID] {
		return nil, nil, errors.New("claim response lost")
	}
	return marshalIssueEnvelope(issue), nil, nil
}

func (r *dispatcherRunner) update(workID string, args []string) ([]byte, []byte, error) {
	issue := r.issues[workID]
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--set-metadata" {
			continue
		}
		key, value, _ := strings.Cut(args[i+1], "=")
		issue.Metadata[key] = value
	}
	if issue.Metadata["ocman.terminal_outcome"] != "" {
		r.commands = append(r.commands, "terminal-metadata:"+workID)
		if r.onTerminalWrite != nil {
			if err := r.onTerminalWrite(workID, issue.Metadata["ocman.attempt_id"]); err != nil {
				return nil, nil, err
			}
		}
	}
	r.issues[workID] = issue
	return marshalIssueEnvelope(issue), nil, nil
}

func (r *dispatcherRunner) close(workID string) ([]byte, []byte, error) {
	issue := r.issues[workID]
	if r.onClose != nil {
		if err := r.onClose(workID); err != nil {
			return nil, nil, err
		}
	}
	issue.Status = "closed"
	r.issues[workID] = issue
	r.commands = append(r.commands, "close:"+workID)
	return marshalIssueEnvelope(issue), nil, nil
}

func (r *dispatcherRunner) snapshot(id string) beadsIssue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneDispatcherIssue(r.issues[id])
}

func (r *dispatcherRunner) commandCount(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, command := range r.commands {
		if strings.HasPrefix(command, prefix) {
			count++
		}
	}
	return count
}

func cloneDispatcherIssue(issue beadsIssue) beadsIssue {
	metadata := issue.Metadata
	issue.Metadata = map[string]string{}
	for key, value := range metadata {
		issue.Metadata[key] = value
	}
	issue.Dependencies = append(issue.Dependencies[:0:0], issue.Dependencies...)
	return issue
}

func hasDispatcherArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func dispatcherArgAfter(args []string, want string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == want {
			return args[i+1]
		}
	}
	return ""
}

func dispatcherMetadataValue(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--set-metadata" {
			name, value, _ := strings.Cut(args[i+1], "=")
			if name == key {
				return value
			}
		}
	}
	return ""
}

type dispatcherExecutor struct {
	mu       sync.Mutex
	db       *state.DB
	err      error
	unsafe   bool
	calls    []string
	observed []model.FactoryAttemptPhase
}

func (e *dispatcherExecutor) ReplaySafe() bool { return !e.unsafe }

func (e *dispatcherExecutor) Execute(ctx context.Context, req FactoryExecutionRequest) (model.FactoryAttemptResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, req.WorkID)
	if e.err != nil {
		return model.FactoryAttemptResult{}, e.err
	}
	if e.db != nil {
		attempt, ok, err := e.db.GetFactoryAttempt(ctx, req.AttemptID)
		if err != nil || !ok {
			return model.FactoryAttemptResult{}, fmt.Errorf("read active attempt: ok=%t: %w", ok, err)
		}
		e.observed = append(e.observed, attempt.Phase)
	}
	return model.FactoryAttemptResult{SchemaVersion: 1, Summary: "completed " + req.WorkID}, nil
}

func (e *dispatcherExecutor) snapshot() ([]string, []model.FactoryAttemptPhase) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...), append([]model.FactoryAttemptPhase(nil), e.observed...)
}

type recordingAttemptStore struct {
	*state.DB
	mu     sync.Mutex
	audits []model.AuditRecord
}

func (s *recordingAttemptStore) AppendFactoryAuditOnce(ctx context.Context, record model.AuditRecord) error {
	if err := s.DB.AppendFactoryAuditOnce(ctx, record); err != nil {
		return err
	}
	s.mu.Lock()
	s.audits = append(s.audits, record)
	s.mu.Unlock()
	return nil
}

func openDispatcherState(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func dispatcherEpic(graph PlanGraph) WorkEpic {
	approval := &PlanApproval{Revision: 3, Hash: hashPlanGraph(graph), FormulaID: DefaultFormulaID, FormulaVersion: 2, FormulaHash: "formula-hash", FormulaOrigin: string(FormulaOriginBuiltIn), InstantiationID: "fixture", Graph: graph}
	return WorkEpic{ID: "epic-1", Status: "open", InitialProject: "/repo", FormulaID: DefaultFormulaID, FormulaRevision: 2, FormulaVersion: 2, FormulaHash: "formula-hash", FormulaOrigin: FormulaOriginBuiltIn, InstantiationID: "fixture", Planning: PlanningState{WorkID: "planning-1", ApprovalGateID: "approval-1"}, Plan: Plan{SchemaVersion: 1, Revision: 3, Hash: approval.Hash, State: PlanApproved, Draft: graph, Approval: approval}}
}

func seedDispatcherEpic(r *dispatcherRunner, epic WorkEpic) {
	plan, _ := json.Marshal(epic.Plan)
	provenance := map[string]string{
		"ocman.contract": "1", "ocman.formula_id": epic.FormulaID, "ocman.formula_revision": "2", "ocman.formula_hash": epic.FormulaHash,
		"ocman.formula_origin": string(epic.FormulaOrigin), "ocman.instantiation_id": epic.InstantiationID,
	}
	epicMeta := cloneDispatcherMetadata(provenance)
	epicMeta["ocman.kind"], epicMeta["ocman.planning_work_id"], epicMeta["ocman.plan_approval_gate_id"] = "work-epic", epic.Planning.WorkID, epic.Planning.ApprovalGateID
	epicMeta["ocman.plan"], epicMeta["ocman.initial_project"], epicMeta["ocman.goal"] = string(plan), epic.InitialProject, "goal"
	planningMeta := cloneDispatcherMetadata(provenance)
	planningMeta["ocman.kind"], planningMeta["ocman.work_epic_id"], planningMeta["ocman.permission_profile"] = "agent-work", epic.ID, planningProfile
	approvalMeta := cloneDispatcherMetadata(provenance)
	approvalMeta["ocman.kind"], approvalMeta["ocman.work_epic_id"], approvalMeta["ocman.gate_type"] = "gate", epic.ID, "plan-approval"
	r.issues[epic.ID] = beadsIssue{ID: epic.ID, Status: "open", IssueType: "epic", Metadata: epicMeta}
	r.issues[epic.Planning.WorkID] = beadsIssue{ID: epic.Planning.WorkID, Status: "closed", IssueType: "task", Metadata: planningMeta}
	r.issues[epic.Planning.ApprovalGateID] = beadsIssue{ID: epic.Planning.ApprovalGateID, Status: "closed", IssueType: "gate", Metadata: approvalMeta}
}

func seedDispatcherWork(r *dispatcherRunner, epic WorkEpic, item PlanItem, id string, priority int, created time.Time) {
	target, _ := planTarget(epic.Plan.Approval.Graph, item.TargetID)
	r.issues[id] = beadsIssue{ID: id, Title: item.Title, Status: "open", Priority: priority, IssueType: "task", CreatedAt: created, Metadata: approvedPlanItemMetadata(epic, item, target)}
}

func cloneDispatcherMetadata(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func TestApprovedPlanMaterializationIsIdempotentAndPreservesBlockingDependencies(t *testing.T) {
	graph := PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{
		{ID: "build", Kind: "agent-work", Title: "Build", TargetID: "target", Profile: "factory-implement/v1"},
		{ID: "verify", Kind: "gate", Title: "Verify", TargetID: "target", GateType: "verification"},
	}, Dependencies: []PlanDependency{{From: "build", To: "verify"}}}
	epic := dispatcherEpic(graph)
	runner := newDispatcherRunner()
	svc := newWithRunner(t.TempDir(), runner, nil)
	beadsDir := filepath.Join(svc.dir, "beads")

	if err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", beadsDir, epic); err != nil {
		t.Fatal(err)
	}
	if err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", beadsDir, epic); err != nil {
		t.Fatal(err)
	}
	if runner.commandCount("create:") != 2 || runner.commandCount("dep:") != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	issues := runner.list(false)
	var envelope struct {
		Data []beadsIssue `json:"data"`
	}
	if err := json.Unmarshal(issues, &envelope); err != nil {
		t.Fatal(err)
	}
	var buildID, verifyID string
	for _, issue := range envelope.Data {
		switch issue.Metadata["ocman.plan_item_id"] {
		case "build":
			buildID = issue.ID
		case "verify":
			verifyID = issue.ID
		}
	}
	if buildID == "" || verifyID == "" || !hasBlockingDependency(envelope.Data, buildID, verifyID) {
		t.Fatalf("materialized dependency %q -> %q missing from %#v", buildID, verifyID, envelope.Data)
	}
}

func TestApprovedPlanMaterializationReconcilesLostResponses(t *testing.T) {
	graph := PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{
		{ID: "build", Kind: "agent-work", Title: "Build", TargetID: "target", Profile: "factory-implement/v1"},
		{ID: "verify", Kind: "gate", Title: "Verify", TargetID: "target", GateType: "provider-check"},
	}, Dependencies: []PlanDependency{{From: "verify", To: "build"}}}
	epic := dispatcherEpic(graph)
	runner := newDispatcherRunner()
	runner.createErrors, runner.dependencyErrors = true, true
	svc := newWithRunner(t.TempDir(), runner, nil)
	if err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"), epic); err != nil {
		t.Fatal(err)
	}
	if runner.commandCount("create:") != 2 || runner.commandCount("dep:") != 1 {
		t.Fatalf("reconciled commands = %v", runner.commands)
	}
}

func TestApprovedPlanMaterializationFailsClosedOnMissingEvidence(t *testing.T) {
	item := PlanItem{ID: "build", Kind: "agent-work", Title: "Build", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	for _, tt := range []struct {
		name string
		runs []fakeRun
	}{
		{name: "create did not commit", runs: []fakeRun{{out: listEnvelope(`[]`)}, {err: errors.New("create failed")}, {out: listEnvelope(`[]`)}}},
		{name: "malformed create response", runs: []fakeRun{{out: listEnvelope(`[]`)}, {out: `{}`}, {out: listEnvelope(`[]`)}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newWithRunner(t.TempDir(), &fakeRunner{runs: tt.runs}, nil)
			if err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"), epic); err == nil {
				t.Fatal("materialization unexpectedly succeeded")
			}
		})
	}

	missingTarget := dispatcherEpic(PlanGraph{Items: []PlanItem{item}})
	svc := newWithRunner(t.TempDir(), newDispatcherRunner(), nil)
	if err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"), missingTarget); !errors.Is(err, ErrPlanIncompatible) {
		t.Fatalf("missing target error = %v", err)
	}

	runner := newDispatcherRunner()
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	seedDispatcherWork(runner, epic, item, "work-2", 1, time.Now().UTC())
	svc = newWithRunner(t.TempDir(), runner, nil)
	if err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"), epic); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate materialization error = %v", err)
	}
}

func TestDispatcherRejectsChangedApprovedGraphAndMaterializedMetadata(t *testing.T) {
	graph := PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{{ID: "build", Kind: "agent-work", Title: "Build", TargetID: "target", Profile: "factory-implement/v1"}}}
	epic := dispatcherEpic(graph)
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)

	corrupt := epic
	corrupt.Plan.Approval.Graph.Items[0].Title = "Changed after approval"
	if err := validateApprovedPlan(corrupt); !errors.Is(err, ErrPlanIncompatible) {
		t.Fatalf("changed approved graph error = %v", err)
	}

	seedDispatcherWork(runner, epic, graph.Items[0], "work-1", 1, time.Now().UTC())
	issue := runner.issues["work-1"]
	issue.Metadata["ocman.permission_profile"] = "factory-review/v1"
	runner.issues[issue.ID] = issue
	svc := newWithRunner(t.TempDir(), runner, nil)
	err := svc.ensureApprovedPlanIssues(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"), epic)
	if err == nil || !strings.Contains(err.Error(), "does not match its approved metadata") {
		t.Fatalf("altered materialization error = %v", err)
	}
}

func TestNextDispatchCandidateOrdersByPriorityCreatedAtAndID(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	tests := []struct {
		name  string
		left  beadsIssue
		right beadsIssue
		want  string
	}{
		{"priority", beadsIssue{ID: "left", Priority: 2, CreatedAt: now}, beadsIssue{ID: "right", Priority: 1, CreatedAt: now.Add(time.Hour)}, "right"},
		{"created_at", beadsIssue{ID: "left", Priority: 1, CreatedAt: now}, beadsIssue{ID: "right", Priority: 1, CreatedAt: now.Add(time.Hour)}, "left"},
		{"ID", beadsIssue{ID: "a", Priority: 1, CreatedAt: now}, beadsIssue{ID: "b", Priority: 1, CreatedAt: now}, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{
				{ID: "one", Kind: "agent-work", Title: "One", TargetID: "target", Profile: "factory-implement/v1"},
				{ID: "two", Kind: "agent-work", Title: "Two", TargetID: "target", Profile: "factory-implement/v1"},
			}}
			epic := dispatcherEpic(graph)
			runner := newDispatcherRunner()
			seedDispatcherEpic(runner, epic)
			seedDispatcherWork(runner, epic, graph.Items[0], tt.left.ID, tt.left.Priority, tt.left.CreatedAt)
			seedDispatcherWork(runner, epic, graph.Items[1], tt.right.ID, tt.right.Priority, tt.right.CreatedAt)
			svc := newWithRunner(t.TempDir(), runner, openDispatcherState(t))
			candidate, ok, err := svc.nextDispatchCandidate(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"))
			if err != nil || !ok || candidate.issue.ID != tt.want {
				t.Fatalf("candidate = %q, %t, %v; want %q", candidate.issue.ID, ok, err, tt.want)
			}
		})
	}
}

func TestStubExecutorAndDispatchStatus(t *testing.T) {
	result, err := (stubFactoryExecutor{}).Execute(context.Background(), FactoryExecutionRequest{WorkID: "work-1"})
	if err != nil || result.SchemaVersion != 1 || !strings.Contains(result.Summary, "work-1") || !(stubFactoryExecutor{}).ReplaySafe() {
		t.Fatalf("stub result = %#v, replaySafe=%t, err=%v", result, (stubFactoryExecutor{}).ReplaySafe(), err)
	}

	db := openDispatcherState(t)
	items := []PlanItem{
		{ID: "ready", Kind: "agent-work", Title: "Ready", TargetID: "target", Profile: "factory-implement/v1"},
		{ID: "running", Kind: "agent-work", Title: "Running", TargetID: "target", Profile: "factory-implement/v1"},
		{ID: "completed", Kind: "agent-work", Title: "Completed", TargetID: "target", Profile: "factory-implement/v1"},
	}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: items})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	for i, item := range items {
		seedDispatcherWork(runner, epic, item, "work-"+item.ID, i+1, time.Unix(int64(i+1), 0).UTC())
	}
	policy := model.FactoryAttemptPolicy{PlanRevision: 3, PlanHash: epic.Plan.Hash, TargetID: "target", Repository: "/repo", Profile: items[0].Profile}
	running, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-running", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(context.Background(), running.ID, model.PlanningSession{}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("activate running = %t, %v", changed, err)
	}
	runningIssue := runner.issues["work-running"]
	runningIssue.Status, runningIssue.Metadata["ocman.attempt_id"] = "in_progress", running.ID
	runner.issues[runningIssue.ID] = runningIssue
	completed, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-completed", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(context.Background(), completed.ID, model.PlanningSession{}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("activate completed = %t, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryAttempt(context.Background(), completed.ID, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done"}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("complete = %t, %v", changed, err)
	}
	completedIssue := runner.issues["work-completed"]
	completedIssue.Status, completedIssue.Metadata["ocman.attempt_id"] = "closed", completed.ID
	completedIssue.Metadata["ocman.terminal_outcome"] = "succeeded"
	runner.issues[completedIssue.ID] = completedIssue
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.owned = true
	status := svc.Status(context.Background())
	if status.Health != HealthHealthy || status.Idle || len(status.Dispatch) != 3 {
		t.Fatalf("status = %#v", status)
	}
	states := map[string]DispatchState{}
	for _, item := range status.Dispatch {
		states[item.ID] = item.State
	}
	if states["work-ready"] != DispatchReady || states["work-running"] != DispatchRunning || states["work-completed"] != DispatchCompleted {
		t.Fatalf("dispatch states = %#v", states)
	}
}

func TestDispatcherLifecyclePersistsTypedEvidenceBeforeBeadsMetadataAndClose(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	runner.onClaim = func(_, attemptID string) error {
		attempt, ok, err := db.GetFactoryAttempt(context.Background(), attemptID)
		if err != nil {
			return fmt.Errorf("read prepared attempt: %w", err)
		}
		if !ok || attempt.Phase != model.FactoryAttemptPrepared {
			return fmt.Errorf("claim observed %#v, ok=%t", attempt, ok)
		}
		return nil
	}
	runner.onTerminalWrite = func(_, attemptID string) error {
		attempt, ok, err := db.GetFactoryAttempt(context.Background(), attemptID)
		if err != nil {
			return fmt.Errorf("read completed attempt: %w", err)
		}
		if !ok || attempt.Phase != model.FactoryAttemptTerminal || attempt.Outcome != model.FactoryAttemptSucceeded || attempt.Result == nil || attempt.Result.SchemaVersion != 1 {
			return fmt.Errorf("metadata observed %#v, ok=%t", attempt, ok)
		}
		return nil
	}
	runner.onClose = func(workID string) error {
		if runner.issues[workID].Metadata["ocman.terminal_outcome"] != "succeeded" {
			return errors.New("close preceded terminal metadata")
		}
		return nil
	}
	executor := &dispatcherExecutor{db: db}
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = executor
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal || attempts[0].Outcome != model.FactoryAttemptSucceeded || attempts[0].Result == nil || attempts[0].Result.SchemaVersion != 1 {
		t.Fatalf("attempts = %#v, err=%v", attempts, err)
	}
	calls, phases := executor.snapshot()
	if !reflect.DeepEqual(calls, []string{"work-1"}) || !reflect.DeepEqual(phases, []model.FactoryAttemptPhase{model.FactoryAttemptActive}) {
		t.Fatalf("execution calls=%v phases=%v", calls, phases)
	}
	issue := runner.snapshot("work-1")
	if issue.Status != "closed" || issue.Metadata["ocman.attempt_id"] != attempts[0].ID || issue.Metadata["ocman.terminal_outcome"] != "succeeded" {
		t.Fatalf("closed issue = %#v", issue)
	}
	if !reflect.DeepEqual(runner.commands[len(runner.commands)-3:], []string{"claim:work-1", "terminal-metadata:work-1", "close:work-1"}) {
		t.Fatalf("command order = %v", runner.commands)
	}
}

func TestFailedClaimTerminatesPreparationAndDispatchesNextWork(t *testing.T) {
	db := openDispatcherState(t)
	items := []PlanItem{
		{ID: "first", Kind: "agent-work", Title: "First", TargetID: "target", Profile: "factory-implement/v1"},
		{ID: "second", Kind: "agent-work", Title: "Second", TargetID: "target", Profile: "factory-implement/v1"},
	}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: items})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, items[0], "work-1", 1, time.Unix(1, 0).UTC())
	seedDispatcherWork(runner, epic, items[1], "work-2", 2, time.Unix(2, 0).UTC())
	runner.claimFailures["work-1"] = true
	executor := &dispatcherExecutor{db: db}
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = executor
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Outcome != model.FactoryAttemptFailed || attempts[0].Failure.Type != "claim_failed" || attempts[1].Outcome != model.FactoryAttemptSucceeded {
		t.Fatalf("attempts = %#v, err=%v", attempts, err)
	}
	calls, _ := executor.snapshot()
	if !reflect.DeepEqual(calls, []string{"work-2"}) {
		t.Fatalf("executed work = %v", calls)
	}
}

func TestDispatcherReconcilesLostClaimResponse(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	runner.claimErrors["work-1"] = true
	svc := newWithRunner(t.TempDir(), runner, db)
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Outcome != model.FactoryAttemptSucceeded || runner.snapshot("work-1").Status != "closed" {
		t.Fatalf("attempts=%#v issue=%#v err=%v", attempts, runner.snapshot("work-1"), err)
	}
}

func TestFailedExecutionPersistsEvidenceAndBlocksRepository(t *testing.T) {
	db := openDispatcherState(t)
	items := []PlanItem{
		{ID: "first", Kind: "agent-work", Title: "First", TargetID: "target", Profile: "factory-implement/v1"},
		{ID: "second", Kind: "agent-work", Title: "Second", TargetID: "target", Profile: "factory-implement/v1"},
	}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: items})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, items[0], "work-1", 1, time.Unix(1, 0).UTC())
	seedDispatcherWork(runner, epic, items[1], "work-2", 2, time.Unix(2, 0).UTC())
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = &dispatcherExecutor{db: db, err: errors.New("stub failed")}
	if err := svc.runDispatcher(context.Background()); err == nil {
		t.Fatal("failed execution unexpectedly succeeded")
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Failure.Type != "stub_execution_failed" {
		t.Fatalf("attempts = %#v, err=%v", attempts, err)
	}
	failed := runner.snapshot("work-1")
	if failed.Status != "in_progress" || failed.Metadata["ocman.terminal_outcome"] != "failed" || failed.Metadata["ocman.failure_type"] != "stub_execution_failed" {
		t.Fatalf("failed Work Item = %#v", failed)
	}
	svc.executor = &dispatcherExecutor{db: db}
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.commandCount("claim:work-2") != 0 {
		t.Fatalf("same-repository work dispatched after failure: %v", runner.commands)
	}
}

func TestFailedAttemptRecoveryRestoresMissingAudit(t *testing.T) {
	db := openDispatcherState(t)
	store := &recordingAttemptStore{DB: db}
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	policy := model.FactoryAttemptPolicy{PlanRevision: 3, PlanHash: epic.Plan.Hash, TargetID: "target", Repository: "/repo", Profile: item.Profile}
	attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-1", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(context.Background(), attempt.ID, model.PlanningSession{}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("activate = %t, %v", changed, err)
	}
	failure := model.FactoryAttemptFailure{Type: "stub_execution_failed", Message: "failed"}
	if changed, err := db.FailFactoryAttempt(context.Background(), attempt.ID, failure, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("fail = %t, %v", changed, err)
	}
	issue := runner.issues["work-1"]
	issue.Status = "in_progress"
	issue.Metadata["ocman.attempt_id"] = attempt.ID
	issue.Metadata["ocman.terminal_outcome"] = "failed"
	issue.Metadata["ocman.failure_type"] = failure.Type
	runner.issues[issue.ID] = issue
	svc := newWithRunner(t.TempDir(), runner, store)
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "attempt.failed" || store.audits[0].AttemptID != attempt.ID {
		t.Fatalf("recovered audits = %#v", store.audits)
	}
}

func TestReconciliationCompletesSuccessfulHalfCommitWithoutExecutingAgain(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	policy := model.FactoryAttemptPolicy{PlanRevision: 3, PlanHash: epic.Plan.Hash, TargetID: "target", Repository: "/repo", Profile: item.Profile}
	attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-1", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(context.Background(), attempt.ID, model.PlanningSession{}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("activate = %t, %v", changed, err)
	}
	result := model.FactoryAttemptResult{SchemaVersion: 1, Summary: "already completed"}
	if changed, err := db.CompleteFactoryAttempt(context.Background(), attempt.ID, result, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("complete = %t, %v", changed, err)
	}
	issue := runner.issues["work-1"]
	issue.Status = "in_progress"
	issue.Metadata["ocman.attempt_id"] = attempt.ID
	runner.issues[issue.ID] = issue
	executor := &dispatcherExecutor{db: db}
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = executor
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls, _ := executor.snapshot(); len(calls) != 0 {
		t.Fatalf("executor called during recovery: %v", calls)
	}
	issue = runner.snapshot("work-1")
	if issue.Status != "closed" || issue.Metadata["ocman.terminal_outcome"] != "succeeded" {
		t.Fatalf("reconciled issue = %#v", issue)
	}
}

func TestReconciliationExecutesCommittedPreparedAttempt(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	policy := model.FactoryAttemptPolicy{PlanRevision: 3, PlanHash: epic.Plan.Hash, TargetID: "target", Repository: "/repo", Profile: item.Profile}
	attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-1", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	issue := runner.issues["work-1"]
	issue.Status, issue.Metadata["ocman.attempt_id"] = "in_progress", attempt.ID
	runner.issues[issue.ID] = issue
	executor := &dispatcherExecutor{db: db}
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = executor
	if err := svc.runDispatcher(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls, phases := executor.snapshot(); !reflect.DeepEqual(calls, []string{"work-1"}) || !reflect.DeepEqual(phases, []model.FactoryAttemptPhase{model.FactoryAttemptActive}) {
		t.Fatalf("calls=%v phases=%v", calls, phases)
	}
	if got := runner.snapshot("work-1"); got.Status != "closed" {
		t.Fatalf("reconciled issue = %#v", got)
	}
}

func TestReconciliationRefusesUnsafeActiveAttemptReplay(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	policy := model.FactoryAttemptPolicy{PlanRevision: 3, PlanHash: epic.Plan.Hash, TargetID: "target", Repository: "/repo", Profile: item.Profile}
	attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-1", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(context.Background(), attempt.ID, model.PlanningSession{}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("activate = %t, %v", changed, err)
	}
	issue := runner.issues["work-1"]
	issue.Status = "in_progress"
	issue.Metadata["ocman.attempt_id"] = attempt.ID
	runner.issues[issue.ID] = issue
	executor := &dispatcherExecutor{db: db, unsafe: true}
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = executor
	err = svc.runDispatcher(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no replay-safe executor") {
		t.Fatalf("recovery error = %v", err)
	}
	if calls, _ := executor.snapshot(); len(calls) != 0 {
		t.Fatalf("unsafe executor was replayed: %v", calls)
	}
	got, ok, err := db.GetFactoryAttempt(context.Background(), attempt.ID)
	if err != nil || !ok || got.Phase != model.FactoryAttemptActive || runner.snapshot("work-1").Status != "in_progress" {
		t.Fatalf("recovery mutated evidence: attempt=%#v ok=%t err=%v issue=%#v", got, ok, err, runner.snapshot("work-1"))
	}
}

func TestStartReconcilesAttemptsWithoutPlanningLauncher(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	policy := model.FactoryAttemptPolicy{PlanRevision: 3, PlanHash: epic.Plan.Hash, TargetID: "target", Repository: "/repo", Profile: item.Profile}
	attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), epic.ID, "work-1", policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(context.Background(), attempt.ID, model.PlanningSession{}, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("activate = %t, %v", changed, err)
	}
	result := model.FactoryAttemptResult{SchemaVersion: 1, Summary: "already completed"}
	if changed, err := db.CompleteFactoryAttempt(context.Background(), attempt.ID, result, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("complete = %t, %v", changed, err)
	}
	issue := runner.issues["work-1"]
	issue.Status = "in_progress"
	issue.Metadata["ocman.attempt_id"] = attempt.ID
	runner.issues[issue.ID] = issue
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.planning = nil
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if got := runner.snapshot("work-1"); got.Status != "closed" {
		t.Fatalf("startup did not reconcile Work Item: %#v", got)
	}
}

func TestReconciliationRejectsRecordlessFactoryClaim(t *testing.T) {
	issue := beadsIssue{ID: "work-1", Status: "in_progress", IssueType: "task", Metadata: map[string]string{"ocman.contract": "1", "ocman.kind": "agent-work", "ocman.plan_item_id": "work"}}
	svc := newWithRunner(t.TempDir(), newDispatcherRunner(issue), openDispatcherState(t))
	err := svc.runDispatcher(context.Background())
	if err == nil || err.Error() != "recordless Factory claim on work-1" {
		t.Fatalf("error = %v", err)
	}
}

func TestReconciliationFailsClosedOnInconsistentAttemptEvidence(t *testing.T) {
	t.Run("claim references missing attempt", func(t *testing.T) {
		issue := beadsIssue{ID: "work-1", Status: "in_progress", Metadata: map[string]string{"ocman.plan_item_id": "work", "ocman.attempt_id": "missing"}}
		svc := newWithRunner(t.TempDir(), newDispatcherRunner(issue), openDispatcherState(t))
		err := svc.reconcileAttempts(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"))
		if err == nil || !strings.Contains(err.Error(), "references missing attempt") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("prepared claim did not commit", func(t *testing.T) {
		db := openDispatcherState(t)
		issue := beadsIssue{ID: "work-1", Status: "open", Metadata: map[string]string{"ocman.plan_item_id": "work"}}
		attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), "epic-1", issue.ID, model.FactoryAttemptPolicy{Repository: "/repo"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		svc := newWithRunner(t.TempDir(), newDispatcherRunner(issue), db)
		if err := svc.reconcileAttempts(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads")); err != nil {
			t.Fatal(err)
		}
		got, ok, err := db.GetFactoryAttempt(context.Background(), attempt.ID)
		if err != nil || !ok || got.Phase != model.FactoryAttemptTerminal || got.Failure.Type != "claim_failed" {
			t.Fatalf("attempt = %#v, ok=%t, err=%v", got, ok, err)
		}
	})

	t.Run("active attempt lost ownership", func(t *testing.T) {
		db := openDispatcherState(t)
		issue := beadsIssue{ID: "work-1", Status: "open", Metadata: map[string]string{"ocman.plan_item_id": "work"}}
		attempt, err := db.CreatePreparedFactoryAttempt(context.Background(), "epic-1", issue.ID, model.FactoryAttemptPolicy{Repository: "/repo"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := db.ActivateFactoryAttempt(context.Background(), attempt.ID, model.PlanningSession{}, time.Now()); err != nil || !changed {
			t.Fatalf("activate = %t, %v", changed, err)
		}
		svc := newWithRunner(t.TempDir(), newDispatcherRunner(issue), db)
		err = svc.reconcileAttempts(context.Background(), "/usr/bin/bd", filepath.Join(svc.dir, "beads"))
		if err == nil || !strings.Contains(err.Error(), "no longer owns") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestConcurrentRunDispatcherClaimsOnce(t *testing.T) {
	db := openDispatcherState(t)
	item := PlanItem{ID: "work", Kind: "agent-work", Title: "Work", TargetID: "target", Profile: "factory-implement/v1"}
	epic := dispatcherEpic(PlanGraph{Targets: []PlanTarget{{ID: "target", Repository: "/repo"}}, Items: []PlanItem{item}})
	runner := newDispatcherRunner()
	seedDispatcherEpic(runner, epic)
	seedDispatcherWork(runner, epic, item, "work-1", 1, time.Now().UTC())
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.executor = &dispatcherExecutor{db: db}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- svc.runDispatcher(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := runner.commandCount("claim:work-1"); got != 1 {
		t.Fatalf("claim count = %d, want 1", got)
	}
	attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, err=%v", attempts, err)
	}
}
