package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
)

type fakeFactoryService struct {
	epic              factory.WorkEpic
	issues            []factory.Issue
	comments          []factory.IssueComment
	commentActor      string
	submitProposalReq factory.SubmitProposalRequest
	proposalEpicID    string
	proposalRevision  int
	proposalsEpicID   string
	formulaID         string
	formulaVersion    int
	previewID         string
	capacityPolicy    factory.CapacityPolicy
	formulas          []factory.NativeFormulaView
	mutation          factory.GraphMutation
	createReq         factory.CreateWorkEpicRequest
	recoveryGate      factory.RecoveryGate
	completedAttempt  string
	completionSummary string
	completionPRURL   string
	err               error
}

func (f *fakeFactoryService) MutateGraph(_ context.Context, mutation factory.GraphMutation) error {
	f.mutation = mutation
	return f.err
}

func (f *fakeFactoryService) CreateWorkEpic(_ context.Context, req factory.CreateWorkEpicRequest) (factory.WorkEpic, error) {
	f.createReq = req
	return factory.WorkEpic{ID: "epic-1", Goal: req.Goal, InitialProject: req.InitialProject}, f.err
}

func (f *fakeFactoryService) CompleteAttempt(_ context.Context, attemptID, _ string, summary, prURL string) error {
	f.completedAttempt, f.completionSummary, f.completionPRURL = attemptID, summary, prURL
	return f.err
}

func (f *fakeFactoryService) ListFormulas(context.Context) ([]factory.NativeFormulaView, error) {
	return f.formulas, nil
}
func (f *fakeFactoryService) ValidateFormula(_ context.Context, source, id string) (factory.NativeFormulaView, error) {
	f.previewID = id
	return factory.NativeFormulaView{Source: source, Valid: true}, f.err
}
func (f *fakeFactoryService) PreviewFormula(_ context.Context, source, id string) (factory.NativeFormulaView, error) {
	f.previewID = id
	return factory.NativeFormulaView{Source: source, Valid: true}, nil
}
func (f *fakeFactoryService) SaveFormula(_ context.Context, req factory.FormulaSaveRequest) (factory.NativeFormulaView, error) {
	if f.err != nil {
		return factory.NativeFormulaView{}, f.err
	}
	formula := factory.NativeFormulaView{ID: req.ID, Version: len(f.formulas) + 1, Source: req.Source, Valid: true}
	f.formulas = append(f.formulas, formula)
	return formula, nil
}

func (f *fakeFactoryService) GetCapacityPolicy(context.Context) (factory.CapacityPolicy, error) {
	return f.capacityPolicy, nil
}
func (f *fakeFactoryService) SetCapacityPolicy(_ context.Context, policy factory.CapacityPolicy) (factory.CapacityPolicy, error) {
	if f.err != nil {
		return factory.CapacityPolicy{}, f.err
	}
	f.capacityPolicy = policy
	return policy, nil
}
func (f *fakeFactoryService) DecidePlanGate(_ context.Context, _ string, action string, req factory.PlanGateDecisionRequest) (factory.PlanGate, error) {
	if f.err != nil {
		return factory.PlanGate{}, f.err
	}
	return factory.PlanGate{Resolution: action, ProposalRevision: req.ExpectedRevision, ProposalHash: req.ExpectedHash}, nil
}
func (f *fakeFactoryService) CreateRecoveryGate(_ context.Context, attemptID, _ string, question, reason string, choices []string) (factory.RecoveryGate, error) {
	if f.err != nil {
		return factory.RecoveryGate{}, f.err
	}
	f.recoveryGate = factory.RecoveryGate{IssueID: "gate-1", AttemptID: attemptID, Question: question, Reason: reason, Choices: choices, Resolution: "open"}
	return f.recoveryGate, nil
}
func (f *fakeFactoryService) ResolveRecoveryGate(_ context.Context, gateID, action, response string) (factory.RecoveryGate, error) {
	if f.err != nil {
		return factory.RecoveryGate{}, f.err
	}
	f.recoveryGate.IssueID, f.recoveryGate.Resolution, f.recoveryGate.Response = gateID, action, response
	return f.recoveryGate, nil
}

func (f *fakeFactoryService) ResolveAuthorityEscalationGate(_ context.Context, gateID, action string) (factory.AuthorityEscalationGate, error) {
	if f.err != nil {
		return factory.AuthorityEscalationGate{}, f.err
	}
	return factory.AuthorityEscalationGate{IssueID: gateID, Resolution: action}, nil
}

func (f *fakeFactoryService) GetFormula(_ context.Context, id string, version int) (factory.NativeFormulaView, error) {
	f.formulaID, f.formulaVersion = id, version
	if id != "ocman/tracer" || version != 1 {
		return factory.NativeFormulaView{}, factory.ErrFormulaNotFound
	}
	return factory.NativeFormulaView{ID: id, Version: version, Source: "name = \"Tracer\"\n", Hash: "hash", Valid: true}, nil
}

func (f *fakeFactoryService) ListWorkEpics(context.Context) ([]factory.WorkEpic, error) {
	return []factory.WorkEpic{f.epic}, nil
}
func (f *fakeFactoryService) GetWorkEpic(context.Context, string) (factory.WorkEpic, error) {
	return f.epic, nil
}
func (f *fakeFactoryService) ListIssues(context.Context, string) ([]factory.Issue, error) {
	return f.issues, nil
}
func (f *fakeFactoryService) ListIssueComments(context.Context, string, string) ([]factory.IssueComment, error) {
	return f.comments, f.err
}
func (f *fakeFactoryService) AddIssueComment(_ context.Context, _, issueID, actor, body string) (factory.IssueComment, error) {
	f.commentActor = actor
	comment := factory.IssueComment{ID: 1, IssueID: issueID, Actor: actor, Body: body}
	f.comments = append(f.comments, comment)
	return comment, f.err
}
func (f *fakeFactoryService) SubmitProposal(_ context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	f.submitProposalReq = req
	return factory.ProposalRevision{EpicID: req.EpicID, Revision: 1}, nil
}
func (f *fakeFactoryService) GetProposal(_ context.Context, epicID string, revision int) (factory.ProposalRevision, error) {
	f.proposalEpicID, f.proposalRevision = epicID, revision
	return factory.ProposalRevision{EpicID: epicID, Revision: revision}, nil
}
func (f *fakeFactoryService) ListProposals(_ context.Context, epicID string) ([]factory.ProposalRevision, error) {
	f.proposalsEpicID = epicID
	return []factory.ProposalRevision{{EpicID: epicID, Revision: 1}}, nil
}

func TestFactoryToolActions(t *testing.T) {
	svc := &fakeFactoryService{issues: []factory.Issue{{ID: "epic-1.child", Kind: "mol", FormulaID: "custom/child", FormulaVersion: 1, FormulaHash: "child-hash", Bindings: map[string]string{"goal": "Ship"}}}}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	got := callTool(t, srv, "factory", map[string]any{"action": "help"})
	if !strings.Contains(resultText(got), "submit_proposal") || !strings.Contains(resultText(got), "mutate_graph") || !strings.Contains(resultText(got), "epicId") || !strings.Contains(resultText(got), "molId") || !strings.Contains(resultText(got), "exactly one node") || !strings.Contains(resultText(got), "Creates the next immutable proposal revision") || !strings.Contains(resultText(got), "validate_formula") || !strings.Contains(resultText(got), "preview_formula") || !strings.Contains(resultText(got), "save_formula") || !strings.Contains(resultText(got), "formula_source") || !strings.Contains(resultText(got), "custom/team") || !strings.Contains(resultText(got), "compiled") || !strings.Contains(resultText(got), "proposals") || !strings.Contains(resultText(got), "sourceHash") || !strings.Contains(resultText(got), "canonical compiled JSON") || !strings.Contains(resultText(got), "example") || !strings.Contains(resultText(got), "output_schema") || !strings.Contains(resultText(got), "action is required") || !strings.Contains(resultText(got), "unknown action") {
		t.Fatalf("help result = %q", resultText(got))
	}
	got = callTool(t, srv, "factory", map[string]any{"action": "issues", "epic_id": "epic-1"})
	if !strings.Contains(resultText(got), `"formulaId": "custom/child"`) || !strings.Contains(resultText(got), `"bindings": {`) || !strings.Contains(resultText(got), `"goal": "Ship"`) {
		t.Fatalf("issues result = %q", resultText(got))
	}
	got = callTool(t, srv, "factory", map[string]any{"action": "add_issue_comment", "epic_id": "epic-1", "issue_id": "epic-1.child", "body": "Reviewed"})
	if got.IsError || svc.commentActor != "mcp" || !strings.Contains(resultText(got), `"body": "Reviewed"`) {
		t.Fatalf("add comment result = %q, actor = %q", resultText(got), svc.commentActor)
	}
	got = callTool(t, srv, "factory", map[string]any{"action": "issue_comments", "epic_id": "epic-1", "issue_id": "epic-1.child"})
	if got.IsError || !strings.Contains(resultText(got), `"body": "Reviewed"`) {
		t.Fatalf("comments result = %q", resultText(got))
	}
	for _, action := range []string{"pour", "claim_plan", "reopen_issue"} {
		if got := callTool(t, srv, "factory", map[string]any{"action": action, "epic_id": "epic-1"}); !got.IsError || resultText(got) != "factory action is not permitted" {
			t.Fatalf("%s result = %q, error = %v", action, resultText(got), got.IsError)
		}
	}
}

func TestFactoryActionRegistryKeepsHelpAndValidationConsistent(t *testing.T) {
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: &fakeFactoryService{}})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	help := callTool(t, srv, "factory", map[string]any{"action": "help"})
	var documented map[string]any
	if err := json.Unmarshal([]byte(resultText(help)), &documented); err != nil {
		t.Fatalf("decode help: %v", err)
	}
	actions, ok := documented["actions"].([]any)
	if !ok || len(actions) == 0 {
		t.Fatalf("actions = %#v", documented["actions"])
	}
	for _, value := range actions {
		action := value.(string)
		detail, ok := documented[action].(map[string]any)
		if !ok || detail["action"] == "" || detail["example"] == "" || detail["output_schema"] == nil || len(detail["errors"].([]any)) == 0 {
			t.Fatalf("incomplete help for %q: %#v", action, detail)
		}
		required := detail["required"].([]any)
		if len(required) > 0 {
			got := callTool(t, srv, "factory", map[string]any{"action": action})
			if !got.IsError || resultText(got) != required[0].(string)+" is required" {
				t.Fatalf("%s missing %s = %q", action, required[0], resultText(got))
			}
		}

		var example map[string]any
		if err := json.Unmarshal([]byte(detail["example"].(string)), &example); err != nil {
			t.Fatalf("decode %s example: %v", action, err)
		}
		got := callTool(t, srv, "factory", example)
		denied := action == "pour" || action == "claim_plan" || action == "reopen_issue"
		if got.IsError != denied {
			t.Fatalf("%s example = %q, error = %v", action, resultText(got), got.IsError)
		}
	}
}

func TestFactoryToolCreatesEpicForPreplannedGraph(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	got := callTool(t, srv, "factory", map[string]any{"action": "create", "goal": "Ship", "brief": "Already broken down", "initial_project": "/repo", "acknowledge_local_execution": true})
	if got.IsError || !strings.Contains(resultText(got), `"id": "epic-1"`) {
		t.Fatalf("create = %q", resultText(got))
	}
	if svc.createReq.Goal != "Ship" || svc.createReq.Brief != "Already broken down" || svc.createReq.InitialProject != "/repo" || !svc.createReq.AcknowledgeLocalExecution || svc.createReq.FormulaID != "" {
		t.Fatalf("create request = %#v", svc.createReq)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "create", "goal": "Ship", "initial_project": "/repo", "acknowledge_local_execution": false}); !got.IsError || resultText(got) != "acknowledge_local_execution must be true" {
		t.Fatalf("unacknowledged create = %q", resultText(got))
	}
}

func TestFactoryToolMutateGraphUsesStrictInput(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	mutation := `{"action":"link","epicId":"epic-1","issueId":"epic-1.1","dependsOnId":"other-1.1","dependencyType":"blocks"}`
	if got := callTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": mutation}); got.IsError {
		t.Fatalf("mutate = %q", resultText(got))
	}
	if svc.mutation != (factory.GraphMutation{Action: "link", EpicID: "epic-1", IssueID: "epic-1.1", DependsOnID: "other-1.1", DependencyType: "blocks", Actor: "mcp"}) {
		t.Fatalf("mutation = %#v", svc.mutation)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": `{"action":"delete","issueId":"x","unexpected":true}`}); !got.IsError || resultText(got) != "mutation_json is invalid" {
		t.Fatalf("unknown mutation field = %q", resultText(got))
	}
}

func TestFactoryToolPlanningActions(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	manifest := `{"epicId":"epic-1","molId":"epic-1","project":"/repo","nodes":[{"key":"implement","type":"implementation","requirement":"required"}]}`
	// Agents must prove they own the Epic: a proposal without the planning
	// attempt's token never reaches the service.
	if got := callTool(t, srv, "factory", map[string]any{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": manifest}); !got.IsError || svc.submitProposalReq.EpicID != "" {
		t.Fatalf("token-less submit = %q (error=%v), service saw %#v", resultText(got), got.IsError, svc.submitProposalReq)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": manifest, "rationale_markdown": "why", "attempt_id": "fa_1", "attempt_token": "fat_1"}); got.IsError {
		t.Fatalf("submit failed: %s", resultText(got))
	}
	want := factory.SubmitProposalRequest{EpicID: "epic-1", Manifest: factory.ProposalManifest{EpicID: "epic-1", MolID: "epic-1", Project: "/repo", Nodes: []factory.ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}, RationaleMarkdown: "why", AttemptID: "fa_1", AttemptToken: "fat_1"}
	if !reflect.DeepEqual(svc.submitProposalReq, want) {
		t.Fatalf("submit = %#v", svc.submitProposalReq)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "proposal", "epic_id": "epic-1", "revision": 2}); got.IsError {
		t.Fatalf("proposal failed: %s", resultText(got))
	}
	if svc.proposalEpicID != "epic-1" || svc.proposalRevision != 2 {
		t.Fatalf("proposal = %q, %d", svc.proposalEpicID, svc.proposalRevision)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "proposals", "epic_id": "epic-1"}); got.IsError || svc.proposalsEpicID != "epic-1" {
		t.Fatalf("proposal history = %q, %q", resultText(got), svc.proposalsEpicID)
	}
	for _, args := range []map[string]any{
		{"action": "claim_plan", "epic_id": "epic-1"},
		{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": "{"},
		{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": `{"epicId":"epic-1","molId":"epic-1","project":"/repo","nodes":[],"unexpected":true}`},
		{"action": "proposal", "epic_id": "epic-1", "revision": 0},
	} {
		if got := callTool(t, srv, "factory", args); !got.IsError {
			t.Fatalf("invalid input accepted: %#v", args)
		}
	}
}

func TestFactoryToolCompletesImplementationAttempt(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	got := callTool(t, srv, "factory", map[string]any{"action": "complete_attempt", "attempt_id": "attempt-1", "attempt_token": "token", "summary": "Implemented and tested.", "pr_url": "https://forge.example/pr/1"})
	if got.IsError || svc.completedAttempt != "attempt-1" || svc.completionSummary != "Implemented and tested." || svc.completionPRURL != "https://forge.example/pr/1" {
		t.Fatalf("completion = %q, %q, %q, %q", resultText(got), svc.completedAttempt, svc.completionSummary, svc.completionPRURL)
	}
}

func TestFactoryToolFormulaAction(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := callTool(t, srv, "factory", map[string]any{"action": "help"}); got.IsError || !strings.Contains(resultText(got), `"composition": "FormulaComposition[]"`) || !strings.Contains(resultText(got), `"errors": "string[]"`) {
		t.Fatalf("formula help = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "formula", "formula_id": "ocman/tracer", "revision": 1}); got.IsError || !strings.Contains(resultText(got), `"id": "ocman/tracer"`) {
		t.Fatalf("formula result = %q", resultText(got))
	}
	if svc.formulaID != "ocman/tracer" || svc.formulaVersion != 1 {
		t.Fatalf("GetFormula(%q, %d)", svc.formulaID, svc.formulaVersion)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "formula", "formula_id": "missing", "revision": 1}); !got.IsError || resultText(got) != "factory Formula not found" {
		t.Fatalf("missing Formula result = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "formula", "formula_id": "ocman/tracer", "revision": 2}); !got.IsError || resultText(got) != "factory Formula not found" {
		t.Fatalf("unknown Formula version result = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "save_formula", "formula_id": "custom/team", "formula_source": `version = 1`}); got.IsError || len(svc.formulas) != 1 {
		t.Fatalf("save Formula result = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "list_formulas"}); got.IsError || !strings.Contains(resultText(got), `"custom/team"`) {
		t.Fatalf("list Formulas result = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "preview_formula", "formula_id": "custom/self", "formula_source": `version = 1`}); got.IsError || svc.previewID != "custom/self" {
		t.Fatalf("preview Formula result = %q", resultText(got))
	}
}

func TestFactoryToolFormulaValidationReturnsFeedback(t *testing.T) {
	svc := &fakeFactoryService{err: fmt.Errorf("%w: TOML source is required", factory.ErrInvalidFormula)}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	got := callTool(t, srv, "factory", map[string]any{"action": "save_formula", "formula_id": "custom/team", "formula_source": "name: YAML"})
	if !got.IsError || resultText(got) != "invalid formula: TOML source is required" {
		t.Fatalf("validation result = %q", resultText(got))
	}
}

func TestFactoryToolCapacityPolicyActions(t *testing.T) {
	svc := &fakeFactoryService{capacityPolicy: factory.CapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{}}}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := callTool(t, srv, "factory", map[string]any{"action": "get_capacity_policy"}); got.IsError || !strings.Contains(resultText(got), `"globalCapacity": 10`) {
		t.Fatalf("get = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "set_capacity_policy", "global_capacity": 12, "project_capacity": 3, "project_overrides": map[string]any{"/repo": 2}}); got.IsError || svc.capacityPolicy.GlobalCapacity != 12 {
		t.Fatalf("set = %q", resultText(got))
	}
	svc.err = errors.New("factory capacity must be between 1 and 1000")
	if got := callTool(t, srv, "factory", map[string]any{"action": "set_capacity_policy", "global_capacity": 0, "project_capacity": 3, "project_overrides": map[string]any{}}); !got.IsError || svc.capacityPolicy.GlobalCapacity != 12 {
		t.Fatalf("invalid set = %q; policy = %#v", resultText(got), svc.capacityPolicy)
	}
}

func TestFactoryToolPlanGateActionsRequireExactProposal(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := callTool(t, srv, "factory", map[string]any{"action": "approve_plan", "epic_id": "epic-1", "revision": 2, "expected_hash": "hash-2"}); got.IsError || !strings.Contains(resultText(got), `"resolution": "approve"`) {
		t.Fatalf("approve = %q", resultText(got))
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "reject_plan", "epic_id": "epic-1", "revision": 2}); !got.IsError || resultText(got) != "expected_hash is required" {
		t.Fatalf("missing hash = %q", resultText(got))
	}
}

func TestFactoryToolRecoveryActions(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := callTool(t, srv, "factory", map[string]any{"action": "request_recovery", "attempt_id": "fa-1", "attempt_token": "token", "question": "Which API?", "reason": "The choice changes persisted data.", "choices_json": `["A","B"]`}); got.IsError || svc.recoveryGate.AttemptID != "fa-1" || len(svc.recoveryGate.Choices) != 2 {
		t.Fatalf("request recovery = %q, %#v", resultText(got), svc.recoveryGate)
	}
	if got := callTool(t, srv, "factory", map[string]any{"action": "request_recovery", "attempt_id": "fa-1", "attempt_token": "token", "question": "Q", "reason": "R", "choices_json": `{}`}); !got.IsError || resultText(got) != "choices_json is invalid" {
		t.Fatalf("invalid choices = %q", resultText(got))
	}
	for _, action := range []string{"resume_recovery", "retry_recovery", "cancel_recovery"} {
		if got := callTool(t, srv, "factory", map[string]any{"action": action, "recovery_gate_id": "gate-1", "response": "Proceed"}); got.IsError || svc.recoveryGate.Resolution != strings.TrimSuffix(action, "_recovery") {
			t.Fatalf("%s = %q, %#v", action, resultText(got), svc.recoveryGate)
		}
	}
}
