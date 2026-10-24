package factorymod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/testutil"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type fakeFactoryService struct {
	epic              factory.WorkEpic
	issues            []factory.Issue
	comments          []factory.IssueComment
	commentActor      string
	submitProposalReq factory.SubmitProposalRequest
	scopeProposalReq  factory.SubmitProposalRequest
	projectRequest    []string
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
	reopenedEpicID    string
	reopenedIssueID   string
	controlCalls      []string
}

func (f *fakeFactoryService) ReopenIssue(_ context.Context, epicID, issueID string) error {
	f.reopenedEpicID, f.reopenedIssueID = epicID, issueID
	return f.err
}

func (f *fakeFactoryService) MutateGraph(_ context.Context, mutation factory.GraphMutation) error {
	f.mutation = mutation
	return f.err
}

func (f *fakeFactoryService) ProposeFactoryControl(_ context.Context, epicID, action string, args map[string]string, summary string) (string, error) {
	f.controlCalls = append(f.controlCalls, fmt.Sprintf("%s:%s:%v:%s", epicID, action, args, summary))
	return "fca_1", f.err
}

func TestFactoryControlToolOnlyProposes(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	got := testutil.CallTool(t, srv, "factory_control", map[string]any{"action": "reopen", "epic_id": "epic-1", "issue_id": "epic-1.3", "summary": "Run the tests again"})
	if got.IsError || len(got.Content) != 2 || !strings.Contains(testutil.ResultText(got), "awaiting_confirmation") {
		t.Fatalf("proposal = %#v", got)
	}
	card := got.Content[1].(mcplib.TextContent).Text
	if !strings.Contains(card, "[[ocman:card type=factory-issue epic=epic-1 issue=fca_1 action=control]]") || !strings.Contains(card, "Do not repeat") {
		t.Fatalf("card = %q", card)
	}
	if want := []string{"epic-1:reopen:map[issue_id:epic-1.3]:Run the tests again"}; !reflect.DeepEqual(svc.controlCalls, want) || svc.reopenedIssueID != "" {
		t.Fatalf("calls = %q, reopened %q", svc.controlCalls, svc.reopenedIssueID)
	}
	svc.err = errors.New("summary is required")
	if got := testutil.CallTool(t, srv, "factory_control", map[string]any{"action": "pause", "epic_id": "epic-1"}); !got.IsError {
		t.Fatal("rejected proposal reported success")
	}
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
	return factory.PlanGate{Resolution: action, ProposalRevision: req.ExpectedRevision, ProposalHash: req.ExpectedHash, ImplementationModel: req.ImplementationModel}, nil
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
	return factory.ProposalRevision{EpicID: req.EpicID, Revision: 1}, f.err
}

func (f *fakeFactoryService) SubmitScopePlan(_ context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	f.scopeProposalReq = req
	return factory.ProposalRevision{EpicID: req.EpicID, Revision: 1}, f.err
}

func (f *fakeFactoryService) RequestProject(_ context.Context, attemptID, token, project, reason string) (factory.ProjectRequestGate, error) {
	f.projectRequest = []string{attemptID, token, project, reason}
	if f.err != nil {
		return factory.ProjectRequestGate{}, f.err
	}
	return factory.ProjectRequestGate{IssueID: "gate-1", EpicID: "epic-1", AttemptID: attemptID, RequestedProject: project, Reason: reason, Resolution: "open"}, nil
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
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "help"})
	if !strings.Contains(testutil.ResultText(got), "submit_proposal") || !strings.Contains(testutil.ResultText(got), "mutate_graph") || !strings.Contains(testutil.ResultText(got), "epicId") || !strings.Contains(testutil.ResultText(got), "molId") || !strings.Contains(testutil.ResultText(got), "dependsOn") || !strings.Contains(testutil.ResultText(got), "on_failure") || !strings.Contains(testutil.ResultText(got), "At least one node") || !strings.Contains(testutil.ResultText(got), "issue-graph proposal revision") || !strings.Contains(testutil.ResultText(got), "validate_formula") || !strings.Contains(testutil.ResultText(got), "preview_formula") || !strings.Contains(testutil.ResultText(got), "save_formula") || !strings.Contains(testutil.ResultText(got), "formula_source") || !strings.Contains(testutil.ResultText(got), "custom/team") || !strings.Contains(testutil.ResultText(got), "compiled") || !strings.Contains(testutil.ResultText(got), "proposals") || !strings.Contains(testutil.ResultText(got), "sourceHash") || !strings.Contains(testutil.ResultText(got), "canonical compiled JSON") || !strings.Contains(testutil.ResultText(got), "example") || !strings.Contains(testutil.ResultText(got), "output_schema") || !strings.Contains(testutil.ResultText(got), "action is required") || !strings.Contains(testutil.ResultText(got), "unknown action") {
		t.Fatalf("help result = %q", testutil.ResultText(got))
	}
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "issues", "epic_id": "epic-1"})
	if !strings.Contains(testutil.ResultText(got), `"formulaId": "custom/child"`) || !strings.Contains(testutil.ResultText(got), `"bindings": {`) || !strings.Contains(testutil.ResultText(got), `"goal": "Ship"`) {
		t.Fatalf("issues result = %q", testutil.ResultText(got))
	}
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "add_issue_comment", "epic_id": "epic-1", "issue_id": "epic-1.child", "body": "Reviewed"})
	if got.IsError || svc.commentActor != "mcp" || !strings.Contains(testutil.ResultText(got), `"body": "Reviewed"`) {
		t.Fatalf("add comment result = %q, actor = %q", testutil.ResultText(got), svc.commentActor)
	}
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "issue_comments", "epic_id": "epic-1", "issue_id": "epic-1.child"})
	if got.IsError || !strings.Contains(testutil.ResultText(got), `"body": "Reviewed"`) {
		t.Fatalf("comments result = %q", testutil.ResultText(got))
	}
	// Creating an epic pours its graph, so pour is no longer an action.
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "pour", "epic_id": "epic-1"}); !got.IsError || testutil.ResultText(got) != "unknown action" {
		t.Fatalf("pour result = %q, error = %v", testutil.ResultText(got), got.IsError)
	}
	for _, action := range []string{"claim_plan", "reopen_issue"} {
		if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": action, "epic_id": "epic-1"}); !got.IsError || testutil.ResultText(got) != "factory action is not permitted" {
			t.Fatalf("%s result = %q, error = %v", action, testutil.ResultText(got), got.IsError)
		}
	}
}

func TestFactoryActionRegistryKeepsHelpAndValidationConsistent(t *testing.T) {
	srv, err := mcptest.NewServer(t, testTools(&fakeFactoryService{})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	help := testutil.CallTool(t, srv, "factory", map[string]any{"action": "help"})
	var documented map[string]any
	if err := json.Unmarshal([]byte(testutil.ResultText(help)), &documented); err != nil {
		t.Fatalf("decode help: %v", err)
	}
	if guidance, ok := documented["reply_cards"].(string); !ok || !strings.Contains(guidance, "Never copy") || !strings.Contains(guidance, "[[ocman:card") {
		t.Fatalf("missing card emission guidance: %#v", documented["reply_cards"])
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
			got := testutil.CallTool(t, srv, "factory", map[string]any{"action": action})
			if !got.IsError || testutil.ResultText(got) != required[0].(string)+" is required" {
				t.Fatalf("%s missing %s = %q", action, required[0], testutil.ResultText(got))
			}
		}

		var example map[string]any
		if err := json.Unmarshal([]byte(detail["example"].(string)), &example); err != nil {
			t.Fatalf("decode %s example: %v", action, err)
		}
		got := testutil.CallTool(t, srv, "factory", example)
		denied := action == "claim_plan" || action == "reopen_issue"
		if got.IsError != denied {
			t.Fatalf("%s example = %q, error = %v", action, testutil.ResultText(got), got.IsError)
		}
		if !denied && action != "create" && action != "import_proposal" && action != "request_project" && action != "mutate_graph" && action != "submit_scope_plan" && len(got.Content) != 1 {
			t.Fatalf("%s unexpectedly emitted extra card guidance: %#v", action, got.Content)
		}
	}
}

func TestDeniedFactoryActionsOfferHumanCards(t *testing.T) {
	svc := &fakeFactoryService{err: factory.ErrActionNotPermitted, epic: factory.WorkEpic{ID: "epic-1"}, issues: []factory.Issue{{ID: "gate-1"}}}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		args   map[string]any
		target string
	}{
		{map[string]any{"action": "claim_plan", "epic_id": "epic-1", "issue_id": "epic-1.1"}, "type=factory-issue epic=epic-1 issue=epic-1.1"},
		{map[string]any{"action": "reopen_issue", "epic_id": "epic-1", "issue_id": "epic-1.3"}, "type=factory-issue epic=epic-1 issue=epic-1.3"},
		{map[string]any{"action": "reopen_issue"}, "type=factory-epic"},
		{map[string]any{"action": "create", "epic_id": "new-epic", "goal": "Ship", "initial_project": "/repo", "formula_id": "custom/team", "acknowledge_local_execution": true}, "type=factory-epic"},
		{map[string]any{"action": "approve_plan", "epic_id": "epic-1", "revision": 1, "expected_hash": "hash"}, "type=factory-epic epic=epic-1"},
		{map[string]any{"action": "revise_plan", "epic_id": "epic-1", "revision": 1, "expected_hash": "hash"}, "type=factory-epic epic=epic-1"},
		{map[string]any{"action": "reject_plan", "epic_id": "epic-1", "revision": 1, "expected_hash": "hash"}, "type=factory-epic epic=epic-1"},
		{map[string]any{"action": "resume_recovery", "recovery_gate_id": "gate-1", "response": "Use A"}, "type=factory-issue epic=epic-1 issue=gate-1"},
		{map[string]any{"action": "retry_recovery", "recovery_gate_id": "gate-1", "response": "Retry"}, "type=factory-issue epic=epic-1 issue=gate-1"},
		{map[string]any{"action": "cancel_recovery", "recovery_gate_id": "gate-1", "response": "Cancel"}, "type=factory-issue epic=epic-1 issue=gate-1"},
		{map[string]any{"action": "approve_authority", "authority_gate_id": "gate-1"}, "type=factory-issue epic=epic-1 issue=gate-1"},
		{map[string]any{"action": "reject_authority", "authority_gate_id": "gate-1"}, "type=factory-issue epic=epic-1 issue=gate-1"},
		{map[string]any{"action": "approve_authority", "authority_gate_id": "missing"}, "type=factory-epic"},
		{map[string]any{"action": "save_formula", "formula_id": "custom/team", "formula_source": "version = 1"}, "type=factory-epic"},
		{map[string]any{"action": "set_capacity_policy", "global_capacity": 1, "project_capacity": 1, "project_overrides": map[string]any{}}, "type=factory-epic"},
		{map[string]any{"action": "mutate_graph", "mutation_json": `{"action":"unlink","epicId":"epic-1","issueId":"epic-1.3","dependsOnId":"epic-1.2"}`}, "type=factory-issue epic=epic-1 issue=epic-1.3"},
		{map[string]any{"action": "claim_plan", "epic_id": "a)b", "issue_id": "x&y"}, "type=factory-issue epic=a%29b issue=x&y"},
	} {
		t.Run(tc.args["action"].(string)+tc.target, func(t *testing.T) {
			got := testutil.CallTool(t, srv, "factory", tc.args)
			if !got.IsError || len(got.Content) != 2 {
				t.Fatalf("denied result = %#v", got)
			}
			card := got.Content[1].(mcplib.TextContent).Text
			marker := "[[ocman:card " + tc.target + " action=" + tc.args["action"].(string) + "]]"
			if !strings.Contains(card, marker) || !strings.Contains(card, "human") || !strings.Contains(card, "they must handle it there") || !strings.Contains(card, "Do not copy the marker") {
				t.Fatalf("card = %q, want %q", card, marker)
			}
		})
	}
	if svc.reopenedEpicID != "" {
		t.Fatal("denied MCP call reopened work")
	}
}

func TestFactoryToolCreatesEpicForPreplannedGraph(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "create", "epic_id": "pretty-epic-ids", "goal": "Ship", "brief": "Already broken down", "initial_project": "/repo", "acknowledge_local_execution": true, "projects": []any{map[string]any{"path": "/docs", "acknowledgeLocalExecution": true}}})
	if got.IsError || !strings.Contains(testutil.ResultText(got), `"id": "epic-1"`) {
		t.Fatalf("create = %q", testutil.ResultText(got))
	}
	guidance := got.Content[1].(mcplib.TextContent).Text
	if !strings.Contains(guidance, "Do not copy the marker") || !strings.Contains(guidance, "[[ocman:card type=factory-epic epic=epic-1 action=created]]") {
		t.Fatalf("creation card guidance must be scoped to this response: %q", guidance)
	}
	if svc.createReq.Goal != "Ship" || svc.createReq.Brief != "Already broken down" || svc.createReq.InitialProject != "/repo" || !svc.createReq.AcknowledgeLocalExecution || svc.createReq.FormulaID != "" || svc.createReq.EpicID != "pretty-epic-ids" || !reflect.DeepEqual(svc.createReq.Projects, []factory.ProjectAdmission{{Path: "/docs", AcknowledgeLocalExecution: true}}) {
		t.Fatalf("create request = %#v", svc.createReq)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "create", "goal": "Ship", "initial_project": "/repo", "acknowledge_local_execution": true, "projects": []any{42}}); !got.IsError || testutil.ResultText(got) != "projects is invalid" {
		t.Fatalf("invalid projects = %q", testutil.ResultText(got))
	}
	svc.err = fmt.Errorf("%w: %q", factory.ErrEpicIDTaken, "pretty-epic-ids")
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "create", "epic_id": "pretty-epic-ids", "goal": "Ship", "initial_project": "/repo", "acknowledge_local_execution": true}); !got.IsError || !strings.Contains(testutil.ResultText(got), "pick another human-friendly id") {
		t.Fatalf("taken epic_id = %q", testutil.ResultText(got))
	}
	svc.err = nil
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "create", "goal": "Ship", "initial_project": "/repo", "acknowledge_local_execution": false}); !got.IsError || testutil.ResultText(got) != "acknowledge_local_execution must be true" {
		t.Fatalf("unacknowledged create = %q", testutil.ResultText(got))
	}
}

func TestFactoryToolMutateGraphUsesStrictInput(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	mutation := `{"action":"link","epicId":"epic-1","issueId":"epic-1.1","dependsOnId":"other-1.1","dependencyType":"blocks","project":"/other"}`
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": mutation}); got.IsError || len(got.Content) != 2 || !strings.Contains(got.Content[1].(mcplib.TextContent).Text, "action=approve_plan") || !strings.Contains(testutil.ResultText(got), "awaiting_approval") {
		t.Fatalf("mutate = %q", testutil.ResultText(got))
	}
	if !reflect.DeepEqual(svc.mutation, factory.GraphMutation{Action: "link", EpicID: "epic-1", IssueID: "epic-1.1", DependsOnID: "other-1.1", DependencyType: "blocks", Project: "/other", Actor: "mcp"}) {
		t.Fatalf("mutation = %#v", svc.mutation)
	}
	batch := `{"action":"batch","epicId":"epic-1","rationaleMarkdown":"## Changes\nAdd coverage.\n\n## Why\nMissing cases.","mutations":[{"action":"create","parentId":"epic-1.1","kind":"task","title":"Coverage"}]}`
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": batch}); got.IsError || len(svc.mutation.Mutations) != 1 || svc.mutation.RationaleMarkdown == "" || svc.mutation.Actor != "mcp" {
		t.Fatalf("batch = %q, %#v", testutil.ResultText(got), svc.mutation)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": `{"action":"batch","epicId":"epic-1","mutations":[{"action":"delete","unexpected":true}]}`}); !got.IsError {
		t.Fatal("unknown batch fields accepted")
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": `{"action":"delete","issueId":"x","unexpected":true}`}); !got.IsError || testutil.ResultText(got) != "mutation_json is invalid" {
		t.Fatalf("unknown mutation field = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "mutate_graph", "mutation_json": `{"action":"delete","issueId":"x","project":42}`}); !got.IsError || testutil.ResultText(got) != "mutation_json is invalid" {
		t.Fatalf("non-string project = %q", testutil.ResultText(got))
	}
}

func TestFactoryToolPlanningActions(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	manifest := `{"epicId":"epic-1","molId":"epic-1","project":"/repo","nodes":[{"key":"implement","type":"implementation","requirement":"required","project":"/other"},{"key":"default","type":"implementation","requirement":"required"}]}`
	// Agents must prove they own the Epic: a proposal without the planning
	// attempt's token never reaches the service.
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": manifest}); !got.IsError || svc.submitProposalReq.EpicID != "" {
		t.Fatalf("token-less submit = %q (error=%v), service saw %#v", testutil.ResultText(got), got.IsError, svc.submitProposalReq)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": manifest, "rationale_markdown": "why", "attempt_id": "fa_1", "attempt_token": "fat_1"}); got.IsError {
		t.Fatalf("submit failed: %s", testutil.ResultText(got))
	}
	want := factory.SubmitProposalRequest{EpicID: "epic-1", Manifest: factory.ProposalManifest{EpicID: "epic-1", MolID: "epic-1", Project: "/repo", Nodes: []factory.ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required", Project: "/other"}, {Key: "default", Type: "implementation", Requirement: "required"}}}, RationaleMarkdown: "why", AttemptID: "fa_1", AttemptToken: "fat_1"}
	if !reflect.DeepEqual(svc.submitProposalReq, want) {
		t.Fatalf("submit = %#v", svc.submitProposalReq)
	}
	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "request_project", "attempt_id": "fa_1", "attempt_token": "fat_1", "project": "/other", "reason": "shared code"})
	if got.IsError || len(got.Content) != 2 || !strings.Contains(got.Content[1].(mcplib.TextContent).Text, "action=request_project") {
		t.Fatalf("project request = %#v", got)
	}
	if !reflect.DeepEqual(svc.projectRequest, []string{"fa_1", "fat_1", "/other", "shared code"}) {
		t.Fatalf("project request arguments = %#v", svc.projectRequest)
	}
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "submit_scope_plan", "epic_id": "epic-1", "manifest_json": manifest, "attempt_id": "fa_1", "attempt_token": "fat_1"})
	if got.IsError || len(got.Content) != 2 || !strings.Contains(got.Content[1].(mcplib.TextContent).Text, "action=approve_plan") || !strings.Contains(testutil.ResultText(got), `"epicId": "epic-1"`) || svc.scopeProposalReq.AttemptID != "fa_1" || svc.scopeProposalReq.AttemptToken != "fat_1" || svc.scopeProposalReq.Manifest.Nodes[0].Project != "/other" {
		t.Fatalf("scope Plan = %#v", got)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "proposal", "epic_id": "epic-1", "revision": 2}); got.IsError {
		t.Fatalf("proposal failed: %s", testutil.ResultText(got))
	}
	if svc.proposalEpicID != "epic-1" || svc.proposalRevision != 2 {
		t.Fatalf("proposal = %q, %d", svc.proposalEpicID, svc.proposalRevision)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "proposals", "epic_id": "epic-1"}); got.IsError || svc.proposalsEpicID != "epic-1" {
		t.Fatalf("proposal history = %q, %q", testutil.ResultText(got), svc.proposalsEpicID)
	}
	for _, args := range []map[string]any{
		{"action": "claim_plan", "epic_id": "epic-1"},
		{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": "{"},
		{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": `{"epicId":"epic-1","molId":"epic-1","project":"/repo","nodes":[],"unexpected":true}`},
		{"action": "submit_proposal", "epic_id": "epic-1", "manifest_json": `{"epicId":"epic-1","molId":"epic-1","project":"/repo","nodes":[{"key":"bad","type":"implementation","requirement":"required","project":42}]}`},
		{"action": "submit_scope_plan", "epic_id": "epic-1", "manifest_json": "{", "attempt_id": "fa_1", "attempt_token": "fat_1"},
		{"action": "proposal", "epic_id": "epic-1", "revision": 0},
	} {
		if got := testutil.CallTool(t, srv, "factory", args); !got.IsError {
			t.Fatalf("invalid input accepted: %#v", args)
		}
	}
}

func TestFactoryToolImportsExistingPlan(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	manifest := `{"epicId":"epic-1","molId":"epic-1","project":"/repo","nodes":[{"key":"implement","type":"implementation","requirement":"required"}]}`
	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": manifest, "rationale_markdown": "Already planned"})
	if got.IsError || !svc.submitProposalReq.Import || svc.submitProposalReq.AttemptID != "" || svc.submitProposalReq.AttemptToken != "" || svc.submitProposalReq.RationaleMarkdown != "Already planned" || len(svc.submitProposalReq.Manifest.Nodes) != 1 {
		t.Fatalf("import = %q, service saw %#v", testutil.ResultText(got), svc.submitProposalReq)
	}
	if len(got.Content) != 2 || !strings.Contains(got.Content[1].(mcplib.TextContent).Text, "[[ocman:card type=factory-epic epic=epic-1 action=approve_plan]]") {
		t.Fatalf("missing human approval card: %#v", got.Content)
	}
	for _, args := range []map[string]any{
		{"action": "import_proposal", "manifest_json": manifest},
		{"action": "import_proposal", "epic_id": "epic-1"},
		{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": "{"},
		{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": manifest + `{}`},
		{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": `{"unknown":true}`},
		{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": manifest, "attempt_id": "attempt"},
		{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": manifest, "attempt_token": "token"},
	} {
		svc.submitProposalReq = factory.SubmitProposalRequest{}
		if got := testutil.CallTool(t, srv, "factory", args); !got.IsError || svc.submitProposalReq.Import {
			t.Fatalf("invalid import accepted: %#v", args)
		}
	}
	svc.err = fmt.Errorf("%w: planning work is already claimed", factory.ErrInvalidRequest)
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "import_proposal", "epic_id": "epic-1", "manifest_json": manifest})
	if !got.IsError || !strings.Contains(testutil.ResultText(got), "already claimed") || len(got.Content) != 1 {
		t.Fatalf("import conflict should explain the error without an action card: %#v", got)
	}
}

func TestFactoryToolCompletesImplementationAttempt(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "complete_attempt", "attempt_id": "attempt-1", "attempt_token": "token", "summary": "Implemented and tested.", "pr_url": "https://forge.example/pr/1"})
	if got.IsError || svc.completedAttempt != "attempt-1" || svc.completionSummary != "Implemented and tested." || svc.completionPRURL != "https://forge.example/pr/1" {
		t.Fatalf("completion = %q, %q, %q, %q", testutil.ResultText(got), svc.completedAttempt, svc.completionSummary, svc.completionPRURL)
	}
}

func TestFactoryToolFormulaAction(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "help"}); got.IsError || !strings.Contains(testutil.ResultText(got), `"composition": "FormulaComposition[]"`) || !strings.Contains(testutil.ResultText(got), `"errors": "string[]"`) {
		t.Fatalf("formula help = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "formula", "formula_id": "ocman/tracer", "revision": 1}); got.IsError || !strings.Contains(testutil.ResultText(got), `"id": "ocman/tracer"`) {
		t.Fatalf("formula result = %q", testutil.ResultText(got))
	}
	if svc.formulaID != "ocman/tracer" || svc.formulaVersion != 1 {
		t.Fatalf("GetFormula(%q, %d)", svc.formulaID, svc.formulaVersion)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "formula", "formula_id": "missing", "revision": 1}); !got.IsError || testutil.ResultText(got) != "factory Formula not found" {
		t.Fatalf("missing Formula result = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "formula", "formula_id": "ocman/tracer", "revision": 2}); !got.IsError || testutil.ResultText(got) != "factory Formula not found" {
		t.Fatalf("unknown Formula version result = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "save_formula", "formula_id": "custom/team", "formula_source": `version = 1`}); got.IsError || len(svc.formulas) != 1 {
		t.Fatalf("save Formula result = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "list_formulas"}); got.IsError || !strings.Contains(testutil.ResultText(got), `"custom/team"`) {
		t.Fatalf("list Formulas result = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "preview_formula", "formula_id": "custom/self", "formula_source": `version = 1`}); got.IsError || svc.previewID != "custom/self" {
		t.Fatalf("preview Formula result = %q", testutil.ResultText(got))
	}
}

func TestFactoryToolFormulaValidationReturnsFeedback(t *testing.T) {
	svc := &fakeFactoryService{err: fmt.Errorf("%w: TOML source is required", factory.ErrInvalidFormula)}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "save_formula", "formula_id": "custom/team", "formula_source": "name: YAML"})
	if !got.IsError || testutil.ResultText(got) != "invalid formula: TOML source is required" {
		t.Fatalf("validation result = %q", testutil.ResultText(got))
	}
}

func TestFactoryToolReturnsActionableRequestErrors(t *testing.T) {
	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	svc := &fakeFactoryService{err: fmt.Errorf("%w: factory worktree has uncommitted changes", factory.ErrInvalidRequest)}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "complete_attempt", "attempt_id": "attempt-1", "attempt_token": "token", "summary": "Implemented.", "pr_url": "https://forge.example/pr/1"})
	if !got.IsError || testutil.ResultText(got) != "invalid factory request: factory worktree has uncommitted changes" {
		t.Fatalf("actionable error result = %q", testutil.ResultText(got))
	}

	svc.err = fmt.Errorf("wrapped: %w", factory.ErrVerificationChecksPending)
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "complete_attempt", "attempt_id": "attempt-1", "attempt_token": "token", "summary": "Verified."})
	if got.IsError || !strings.Contains(testutil.ResultText(got), `"status": "checks_running"`) || !strings.Contains(testutil.ResultText(got), "end your turn") {
		t.Fatalf("pending checks result = %q (error %v)", testutil.ResultText(got), got.IsError)
	}

	svc.err = errors.New("database details")
	got = testutil.CallTool(t, srv, "factory", map[string]any{"action": "complete_attempt", "attempt_id": "attempt-1", "attempt_token": "token", "summary": "Implemented.", "pr_url": "https://forge.example/pr/1"})
	if !got.IsError || testutil.ResultText(got) != "database details" {
		t.Fatalf("internal error result = %q", testutil.ResultText(got))
	}
	entry := hook.LastEntry()
	if entry == nil || entry.Message != "Factory MCP request failed" || fmt.Sprint(entry.Data[logrus.ErrorKey]) != svc.err.Error() {
		t.Fatalf("error log = %#v", entry)
	}
}

func TestFactoryToolCapacityPolicyActions(t *testing.T) {
	svc := &fakeFactoryService{capacityPolicy: factory.CapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{}}}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "get_capacity_policy"}); got.IsError || !strings.Contains(testutil.ResultText(got), `"globalCapacity": 10`) {
		t.Fatalf("get = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "set_capacity_policy", "global_capacity": 12, "project_capacity": 3, "project_overrides": map[string]any{"/repo": 2}}); got.IsError || svc.capacityPolicy.GlobalCapacity != 12 {
		t.Fatalf("set = %q", testutil.ResultText(got))
	}
	svc.err = errors.New("factory capacity must be between 1 and 1000")
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "set_capacity_policy", "global_capacity": 0, "project_capacity": 3, "project_overrides": map[string]any{}}); !got.IsError || svc.capacityPolicy.GlobalCapacity != 12 {
		t.Fatalf("invalid set = %q; policy = %#v", testutil.ResultText(got), svc.capacityPolicy)
	}
}

func TestFactoryToolPlanGateActionsRequireExactProposal(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "approve_plan", "epic_id": "epic-1", "revision": 2, "expected_hash": "hash-2", "implementation_model": "openai/gpt-5.6-sol"}); got.IsError || !strings.Contains(testutil.ResultText(got), `"resolution": "approve"`) || !strings.Contains(testutil.ResultText(got), `"implementationModel": "openai/gpt-5.6-sol"`) {
		t.Fatalf("approve = %q", testutil.ResultText(got))
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "reject_plan", "epic_id": "epic-1", "revision": 2}); !got.IsError || testutil.ResultText(got) != "expected_hash is required" {
		t.Fatalf("missing hash = %q", testutil.ResultText(got))
	}
}

func TestFactoryToolRecoveryActions(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "request_recovery", "attempt_id": "fa-1", "attempt_token": "token", "question": "Which API?", "reason": "The choice changes persisted data.", "choices_json": `["A","B"]`}); got.IsError || svc.recoveryGate.AttemptID != "fa-1" || len(svc.recoveryGate.Choices) != 2 {
		t.Fatalf("request recovery = %q, %#v", testutil.ResultText(got), svc.recoveryGate)
	}
	if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": "request_recovery", "attempt_id": "fa-1", "attempt_token": "token", "question": "Q", "reason": "R", "choices_json": `{}`}); !got.IsError || testutil.ResultText(got) != "choices_json is invalid" {
		t.Fatalf("invalid choices = %q", testutil.ResultText(got))
	}
	for _, action := range []string{"resume_recovery", "retry_recovery", "cancel_recovery"} {
		if got := testutil.CallTool(t, srv, "factory", map[string]any{"action": action, "recovery_gate_id": "gate-1", "response": "Proceed"}); got.IsError || svc.recoveryGate.Resolution != strings.TrimSuffix(action, "_recovery") {
			t.Fatalf("%s = %q, %#v", action, testutil.ResultText(got), svc.recoveryGate)
		}
	}
}
