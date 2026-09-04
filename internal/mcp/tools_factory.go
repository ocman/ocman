package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type factoryService interface {
	CreateWorkEpic(context.Context, factory.CreateWorkEpicRequest) (factory.WorkEpic, error)
	ListWorkEpics(context.Context) ([]factory.WorkEpic, error)
	GetWorkEpic(context.Context, string) (factory.WorkEpic, error)
	ListIssues(context.Context, string) ([]factory.Issue, error)
	ListIssueComments(context.Context, string, string) ([]factory.IssueComment, error)
	AddIssueComment(context.Context, string, string, string, string) (factory.IssueComment, error)
	SubmitProposal(context.Context, factory.SubmitProposalRequest) (factory.ProposalRevision, error)
	GetProposal(context.Context, string, int) (factory.ProposalRevision, error)
	ListProposals(context.Context, string) ([]factory.ProposalRevision, error)
	GetFormula(context.Context, string, int) (factory.NativeFormulaView, error)
	ListFormulas(context.Context) ([]factory.NativeFormulaView, error)
	ValidateFormula(context.Context, string, string) (factory.NativeFormulaView, error)
	PreviewFormula(context.Context, string, string) (factory.NativeFormulaView, error)
	SaveFormula(context.Context, factory.FormulaSaveRequest) (factory.NativeFormulaView, error)
	GetCapacityPolicy(context.Context) (factory.CapacityPolicy, error)
	SetCapacityPolicy(context.Context, factory.CapacityPolicy) (factory.CapacityPolicy, error)
	DecidePlanGate(context.Context, string, string, factory.PlanGateDecisionRequest) (factory.PlanGate, error)
	CreateRecoveryGate(context.Context, string, string, string, string, []string) (factory.RecoveryGate, error)
	ResolveRecoveryGate(context.Context, string, string, string) (factory.RecoveryGate, error)
	ResolveAuthorityEscalationGate(context.Context, string, string) (factory.AuthorityEscalationGate, error)
	CompleteAttempt(context.Context, string, string, string, string) error
}
type factoryTools struct{ svc factoryService }

const factoryFormulaExample = `version = 1\nname = \"Team\"\n\n[[input]]\nkey = \"goal\"\n\n[[input]]\nkey = \"initial_project\"\n\n[[issue]]\nkey = \"plan\"\nkind = \"plan\"\n`

type factoryAction struct {
	name, description, example string
	required, optional         []string
	output                     any
	errors                     []string
}

var factoryFormulaOutput = map[string]string{"id": "string", "version": "integer", "name": "string", "source": "string", "hash": "string (canonical compiled JSON)", "sourceHash": "string (TOML source provenance)", "compiled": "object (canonical interchange JSON)", "inputs": "string[]", "nodes": "FormulaGraphNode[]", "edges": "FormulaGraphEdge[]", "composition": "FormulaComposition[]", "valid": "boolean", "errors": "string[]"}

var factoryActions = []factoryAction{
	{name: "help", description: "Describes every available Factory action.", example: `{"action":"help"}`, output: "Factory action documentation", errors: []string{"action is required"}},
	{name: "list", description: "Lists Factory Work Epics.", example: `{"action":"list"}`, output: "WorkEpic[]", errors: []string{"factory request failed"}},
	{name: "get", description: "Gets one Factory Work Epic.", example: `{"action":"get","epic_id":"epic-1"}`, required: []string{"epic_id"}, output: "WorkEpic", errors: []string{"epic_id is required", "factory epic not found", "factory request failed"}},
	{name: "issues", description: "Lists the Issues in one Factory Work Epic.", example: `{"action":"issues","epic_id":"epic-1"}`, required: []string{"epic_id"}, output: "Issue[]", errors: []string{"epic_id is required", "factory epic not found", "factory request failed"}},
	{name: "issue_comments", description: "Lists append-only comments on one Factory Issue.", example: `{"action":"issue_comments","epic_id":"epic-1","issue_id":"epic-1.1"}`, required: []string{"epic_id", "issue_id"}, output: "IssueComment[]", errors: []string{"epic_id is required", "issue_id is required", "factory request failed"}},
	{name: "add_issue_comment", description: "Appends a comment to one Factory Issue.", example: `{"action":"add_issue_comment","epic_id":"epic-1","issue_id":"epic-1.1","body":"Implemented and tested."}`, required: []string{"epic_id", "issue_id", "body"}, output: "IssueComment", errors: []string{"epic_id is required", "issue_id is required", "body is required", "factory request failed"}},
	{name: "formula", description: "Gets one immutable Formula revision.", example: `{"action":"formula","formula_id":"ocman/tracer","revision":1}`, required: []string{"formula_id", "revision"}, output: factoryFormulaOutput, errors: []string{"formula_id is required", "revision is required", "factory Formula not found", "factory request failed"}},
	{name: "list_formulas", description: "Lists immutable Factory Formula revisions.", example: `{"action":"list_formulas"}`, output: []any{factoryFormulaOutput}, errors: []string{"factory request failed"}},
	{name: "validate_formula", description: "Validates TOML Formula source without saving it.", example: `{"action":"validate_formula","formula_source":"` + factoryFormulaExample + `"}`, required: []string{"formula_source"}, optional: []string{"formula_id"}, output: factoryFormulaOutput, errors: []string{"formula_source is required", "invalid formula", "factory request failed"}},
	{name: "preview_formula", description: "Compiles TOML Formula source without saving it.", example: `{"action":"preview_formula","formula_source":"` + factoryFormulaExample + `"}`, required: []string{"formula_source"}, optional: []string{"formula_id"}, output: factoryFormulaOutput, errors: []string{"formula_source is required", "invalid formula", "factory request failed"}},
	{name: "save_formula", description: "Creates or reuses an immutable custom Formula revision.", example: `{"action":"save_formula","formula_id":"custom/team","formula_source":"` + factoryFormulaExample + `"}`, required: []string{"formula_id", "formula_source"}, output: factoryFormulaOutput, errors: []string{"formula_id is required", "formula_source is required", "invalid formula", "factory request failed"}},
	{name: "submit_proposal", description: "Creates the next immutable proposal revision; manifest nodes have unique stable keys and exactly one node must be a required implementation.", example: `{"action":"submit_proposal","epic_id":"epic-1","manifest_json":"{\"epicId\":\"epic-1\",\"molId\":\"epic-1\",\"project\":\"/repo\",\"nodes\":[{\"key\":\"implement\",\"type\":\"implementation\",\"requirement\":\"required\"}]}","attempt_id":"fa_1","attempt_token":"fat_1"}`, required: []string{"epic_id", "manifest_json", "attempt_id", "attempt_token"}, optional: []string{"rationale_markdown"}, output: "ProposalRevision", errors: []string{"epic_id is required", "manifest_json is required", "attempt_id and attempt_token are required", "factory action is not permitted", "manifest_json is invalid", "factory epic not found", "factory request failed"}},
	{name: "proposals", description: "Lists immutable proposal revisions for a Work Epic.", example: `{"action":"proposals","epic_id":"epic-1"}`, required: []string{"epic_id"}, output: "ProposalRevision[]", errors: []string{"epic_id is required", "factory epic not found", "factory request failed"}},
	{name: "proposal", description: "Gets one immutable proposal revision.", example: `{"action":"proposal","epic_id":"epic-1","revision":1}`, required: []string{"epic_id", "revision"}, output: "ProposalRevision", errors: []string{"epic_id is required", "revision is required", "factory epic not found", "factory request failed"}},
	{name: "approve_plan", description: "Approves the exact proposed plan revision.", example: `{"action":"approve_plan","epic_id":"epic-1","revision":1,"expected_hash":"hash"}`, required: []string{"epic_id", "revision", "expected_hash"}, optional: []string{"feedback"}, output: "PlanGate", errors: []string{"epic_id is required", "revision is required", "expected_hash is required", "factory request failed"}},
	{name: "revise_plan", description: "Requests revision of the exact proposed plan.", example: `{"action":"revise_plan","epic_id":"epic-1","revision":1,"expected_hash":"hash","feedback":"Clarify scope"}`, required: []string{"epic_id", "revision", "expected_hash"}, optional: []string{"feedback"}, output: "PlanGate", errors: []string{"epic_id is required", "revision is required", "expected_hash is required", "factory request failed"}},
	{name: "reject_plan", description: "Rejects the exact proposed plan.", example: `{"action":"reject_plan","epic_id":"epic-1","revision":1,"expected_hash":"hash"}`, required: []string{"epic_id", "revision", "expected_hash"}, optional: []string{"feedback"}, output: "PlanGate", errors: []string{"epic_id is required", "revision is required", "expected_hash is required", "factory request failed"}},
	{name: "get_capacity_policy", description: "Gets Factory capacity limits.", example: `{"action":"get_capacity_policy"}`, output: "CapacityPolicy", errors: []string{"factory request failed"}},
	{name: "set_capacity_policy", description: "Sets Factory capacity limits.", example: `{"action":"set_capacity_policy","global_capacity":10,"project_capacity":4,"project_overrides":{"/repo":2}}`, required: []string{"global_capacity", "project_capacity", "project_overrides"}, output: "CapacityPolicy", errors: []string{"global_capacity is required", "project_capacity is required", "project_overrides is invalid", "factory request failed"}},
	{name: "request_recovery", description: "Pauses the assigned implementation Attempt for a human decision while preserving its session and worktree.", example: `{"action":"request_recovery","attempt_id":"fa-1","attempt_token":"token","question":"Which API should I use?","reason":"Both supported APIs change persisted behavior.","choices_json":"[\"API A\",\"API B\"]"}`, required: []string{"attempt_id", "attempt_token", "question", "reason"}, optional: []string{"choices_json"}, output: "RecoveryGate", errors: []string{"attempt_id is required", "attempt_token is required", "question is required", "reason is required", "choices_json is invalid", "factory request failed"}},
	{name: "complete_attempt", description: "Records a clean committed handoff and the shared delivery pull request for the assigned active implementation Attempt.", example: `{"action":"complete_attempt","attempt_id":"fa-1","attempt_token":"token","summary":"Implemented and tested.","pr_url":"https://forge.example/owner/repo/pulls/1"}`, required: []string{"attempt_id", "attempt_token", "summary", "pr_url"}, output: map[string]string{"status": "completed"}, errors: []string{"attempt_id is required", "attempt_token is required", "summary is required", "pr_url is required", "factory request failed"}},
	{name: "resume_recovery", description: "Resumes the paused implementation session with a durable human response.", example: `{"action":"resume_recovery","recovery_gate_id":"gate-1","response":"Use API A."}`, required: []string{"recovery_gate_id"}, optional: []string{"response"}, output: "RecoveryGate", errors: []string{"recovery_gate_id is required", "factory request failed"}},
	{name: "retry_recovery", description: "Ends the paused attempt and starts a fresh implementation Attempt.", example: `{"action":"retry_recovery","recovery_gate_id":"gate-1","response":"Retry from a clean worktree."}`, required: []string{"recovery_gate_id"}, optional: []string{"response"}, output: "RecoveryGate", errors: []string{"recovery_gate_id is required", "factory request failed"}},
	{name: "cancel_recovery", description: "Cancels the paused implementation Issue.", example: `{"action":"cancel_recovery","recovery_gate_id":"gate-1","response":"No longer needed."}`, required: []string{"recovery_gate_id"}, optional: []string{"response"}, output: "RecoveryGate", errors: []string{"recovery_gate_id is required", "factory request failed"}},
	{name: "approve_authority", description: "Approves one out-of-profile permission request exactly once.", example: `{"action":"approve_authority","authority_gate_id":"gate-1"}`, required: []string{"authority_gate_id"}, output: "AuthorityEscalationGate", errors: []string{"authority_gate_id is required", "factory request failed"}},
	{name: "reject_authority", description: "Rejects one out-of-profile permission request exactly once.", example: `{"action":"reject_authority","authority_gate_id":"gate-1"}`, required: []string{"authority_gate_id"}, output: "AuthorityEscalationGate", errors: []string{"authority_gate_id is required", "factory request failed"}},
	{name: "mutate_graph", description: "Creates, edits, reparents, links, unlinks, or soft-deletes local Factory Issues unless they are in progress or closed.", example: `{"action":"mutate_graph","mutation_json":"{\"action\":\"create\",\"epicId\":\"epic-1\",\"parentId\":\"epic-1.1\",\"kind\":\"task\",\"title\":\"Implement the change\"}"}`, required: []string{"mutation_json"}, output: map[string]string{"status": "ok"}, errors: []string{"mutation_json is required", "mutation_json is invalid", "factory request failed"}},
	{name: "create", description: "Creates and pours a Factory Work Epic, using the built-in tracer Formula by default. Use issues and mutate_graph to add an already-planned ticket breakdown.", example: `{"action":"create","goal":"Ship","initial_project":"/repo","acknowledge_local_execution":true}`, required: []string{"goal", "initial_project", "acknowledge_local_execution"}, optional: []string{"brief", "instantiation_id", "formula_id", "revision"}, output: "WorkEpic", errors: []string{"goal is required", "initial_project is required", "acknowledge_local_execution must be true", "factory request failed"}},
	{name: "pour", description: "Retired: pouring Factory graphs is a user action.", example: `{"action":"pour"}`, output: "none", errors: []string{"factory action is not permitted"}},
	{name: "claim_plan", description: "Retired: claiming Factory Planning Work is a user action.", example: `{"action":"claim_plan"}`, output: "none", errors: []string{"factory action is not permitted"}},
	{name: "reopen_issue", description: "Reopening failed or cancelled work is a user action: the operator does it from the Factory action inbox, so ask them instead of retrying.", example: `{"action":"reopen_issue"}`, output: "none", errors: []string{"factory action is not permitted"}},
}

func factoryActionFor(name string) (factoryAction, bool) {
	for _, action := range factoryActions {
		if action.name == name {
			return action, true
		}
	}
	return factoryAction{}, false
}

func factoryHelp() map[string]any {
	help := make(map[string]any, len(factoryActions)+2)
	actions := make([]string, 0, len(factoryActions))
	for _, action := range factoryActions {
		actions = append(actions, action.name)
		help[action.name] = map[string]any{"required": append([]string{}, action.required...), "optional": append([]string{}, action.optional...), "action": action.description, "example": action.example, "output_schema": action.output, "errors": action.errors}
	}
	help["actions"] = actions
	help["errors"] = []string{"action is required", "unknown action"}
	return help
}

func addFactoryTools(s *server.MCPServer, tools *factoryTools) {
	for _, tool := range factoryServerTools(tools) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}
func factoryServerTools(tools *factoryTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("factory", mcplib.WithDescription("Factory Planning Work actions."), mcplib.WithString("action", mcplib.Required()), mcplib.WithString("epic_id"), mcplib.WithString("issue_id"), mcplib.WithString("body"), mcplib.WithString("goal"), mcplib.WithString("brief"), mcplib.WithString("initial_project"), mcplib.WithString("instantiation_id"), mcplib.WithBoolean("acknowledge_local_execution"), mcplib.WithString("formula_id"), mcplib.WithString("formula_source"), mcplib.WithString("manifest_json"), mcplib.WithString("mutation_json"), mcplib.WithString("rationale_markdown"), mcplib.WithString("expected_hash"), mcplib.WithString("feedback"), mcplib.WithString("attempt_id"), mcplib.WithString("attempt_token"), mcplib.WithString("summary"), mcplib.WithString("pr_url"), mcplib.WithString("recovery_gate_id"), mcplib.WithString("authority_gate_id"), mcplib.WithString("question"), mcplib.WithString("reason"), mcplib.WithString("choices_json"), mcplib.WithString("response"), mcplib.WithNumber("revision"), mcplib.WithNumber("global_capacity"), mcplib.WithNumber("project_capacity"), mcplib.WithObject("project_overrides")), Handler: tools.handle}}
}
func (t *factoryTools) handle(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	registered, ok := factoryActionFor(action)
	if !ok {
		return mcplib.NewToolResultError("unknown action"), nil
	}
	arguments, ok := req.Params.Arguments.(map[string]any)
	for _, field := range registered.required {
		if value, present := arguments[field]; !ok || !present || value == nil || value == "" {
			return mcplib.NewToolResultError(field + " is required"), nil
		}
	}
	switch action {
	case "create":
		acknowledged, err := req.RequireBool("acknowledge_local_execution")
		if err != nil || !acknowledged {
			return mcplib.NewToolResultError("acknowledge_local_execution must be true"), nil
		}
		epic, err := t.svc.CreateWorkEpic(ctx, factory.CreateWorkEpicRequest{
			InstantiationID:           req.GetString("instantiation_id", ""),
			Goal:                      req.GetString("goal", ""),
			Brief:                     req.GetString("brief", ""),
			InitialProject:            req.GetString("initial_project", ""),
			FormulaID:                 req.GetString("formula_id", ""),
			FormulaRevision:           req.GetInt("revision", 0),
			AcknowledgeLocalExecution: true,
		})
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(epic), nil
	case "complete_attempt":
		attemptID, _ := req.RequireString("attempt_id")
		token, _ := req.RequireString("attempt_token")
		summary, _ := req.RequireString("summary")
		prURL, _ := req.RequireString("pr_url")
		if err := t.svc.CompleteAttempt(ctx, attemptID, token, summary, prURL); err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(map[string]string{"status": "completed"}), nil
	case "approve_authority", "reject_authority":
		gateID, _ := req.RequireString("authority_gate_id")
		gate, err := t.svc.ResolveAuthorityEscalationGate(ctx, gateID, strings.TrimSuffix(action, "_authority"))
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(gate), nil
	case "request_recovery":
		attemptID, _ := req.RequireString("attempt_id")
		token, _ := req.RequireString("attempt_token")
		question, _ := req.RequireString("question")
		reason, _ := req.RequireString("reason")
		choices, err := factoryRecoveryChoices(req)
		if err != nil {
			return mcplib.NewToolResultError("choices_json is invalid"), nil
		}
		gate, err := t.svc.CreateRecoveryGate(ctx, attemptID, token, question, reason, choices)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(gate), nil
	case "resume_recovery", "retry_recovery", "cancel_recovery":
		gateID, _ := req.RequireString("recovery_gate_id")
		response, _ := req.RequireString("response")
		gate, err := t.svc.ResolveRecoveryGate(ctx, gateID, strings.TrimSuffix(action, "_recovery"), response)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(gate), nil
	case "mutate_graph":
		mutator, ok := t.svc.(interface {
			MutateGraph(context.Context, factory.GraphMutation) error
		})
		if !ok {
			return mcplib.NewToolResultError("factory action is not permitted"), nil
		}
		raw, err := req.RequireString("mutation_json")
		var mutation factory.GraphMutation
		decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
		decoder.DisallowUnknownFields()
		if err != nil || decoder.Decode(&mutation) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return mcplib.NewToolResultError("mutation_json is invalid"), nil
		}
		mutation.Actor = "mcp"
		if err := mutator.MutateGraph(ctx, mutation); err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(map[string]string{"status": "ok"}), nil
	case "help":
		return toolResultJSON(factoryHelp()), nil
	case "get_capacity_policy":
		policy, err := t.svc.GetCapacityPolicy(ctx)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(policy), nil
	case "approve_plan", "revise_plan", "reject_plan":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		revision, err := req.RequireInt("revision")
		if err != nil || revision < 1 {
			return mcplib.NewToolResultError("revision is required"), nil
		}
		hash, err := req.RequireString("expected_hash")
		if err != nil || hash == "" {
			return mcplib.NewToolResultError("expected_hash is required"), nil
		}
		feedback, _ := req.RequireString("feedback")
		gate, err := t.svc.DecidePlanGate(ctx, id, strings.TrimSuffix(action, "_plan"), factory.PlanGateDecisionRequest{ExpectedRevision: revision, ExpectedHash: hash, Feedback: feedback})
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(gate), nil
	case "set_capacity_policy":
		global, err := req.RequireInt("global_capacity")
		if err != nil {
			return mcplib.NewToolResultError("global_capacity is required"), nil
		}
		project, err := req.RequireInt("project_capacity")
		if err != nil {
			return mcplib.NewToolResultError("project_capacity is required"), nil
		}
		overrides, err := factoryProjectOverrides(req)
		if err != nil {
			return mcplib.NewToolResultError("project_overrides is invalid"), nil
		}
		policy, err := t.svc.SetCapacityPolicy(ctx, factory.CapacityPolicy{GlobalCapacity: global, ProjectCapacity: project, ProjectOverrides: overrides})
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(policy), nil
	case "list":
		epics, err := t.svc.ListWorkEpics(ctx)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(epics), nil
	case "get":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		epic, err := t.svc.GetWorkEpic(ctx, id)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(epic), nil
	case "issues":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		issues, err := t.svc.ListIssues(ctx, id)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(issues), nil
	case "issue_comments":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		comments, err := t.svc.ListIssueComments(ctx, id, req.GetString("issue_id", ""))
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(comments), nil
	case "add_issue_comment":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		comment, err := t.svc.AddIssueComment(ctx, id, req.GetString("issue_id", ""), "mcp", req.GetString("body", ""))
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(comment), nil
	case "formula":
		id, err := req.RequireString("formula_id")
		if err != nil || id == "" {
			return mcplib.NewToolResultError("formula_id is required"), nil
		}
		version, err := req.RequireInt("revision")
		if err != nil || version < 1 {
			return mcplib.NewToolResultError("revision is required"), nil
		}
		formula, err := t.svc.GetFormula(ctx, id, version)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(formula), nil
	case "list_formulas":
		formulas, err := t.svc.ListFormulas(ctx)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(formulas), nil
	case "validate_formula", "preview_formula":
		source, err := req.RequireString("formula_source")
		if err != nil || source == "" {
			return mcplib.NewToolResultError("formula_source is required"), nil
		}
		var formula factory.NativeFormulaView
		if action == "validate_formula" {
			formula, err = t.svc.ValidateFormula(ctx, source, optionalFormulaID(req))
		} else {
			formula, err = t.svc.PreviewFormula(ctx, source, optionalFormulaID(req))
		}
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(formula), nil
	case "save_formula":
		id, err := req.RequireString("formula_id")
		if err != nil || id == "" {
			return mcplib.NewToolResultError("formula_id is required"), nil
		}
		source, err := req.RequireString("formula_source")
		if err != nil || source == "" {
			return mcplib.NewToolResultError("formula_source is required"), nil
		}
		formula, err := t.svc.SaveFormula(ctx, factory.FormulaSaveRequest{ID: id, Source: source})
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(formula), nil
	case "submit_proposal":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		manifestJSON, err := req.RequireString("manifest_json")
		if err != nil {
			return mcplib.NewToolResultError("manifest_json is required"), nil
		}
		var manifest factory.ProposalManifest
		decoder := json.NewDecoder(bytes.NewReader([]byte(manifestJSON)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&manifest) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return mcplib.NewToolResultError("manifest_json is invalid"), nil
		}
		rationale, _ := req.RequireString("rationale_markdown")
		attemptID, _ := req.RequireString("attempt_id")
		token, _ := req.RequireString("attempt_token")
		if attemptID == "" || token == "" {
			return mcplib.NewToolResultError("attempt_id and attempt_token are required"), nil
		}
		proposal, err := t.svc.SubmitProposal(ctx, factory.SubmitProposalRequest{EpicID: id, Manifest: manifest, RationaleMarkdown: rationale, AttemptID: attemptID, AttemptToken: token})
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(proposal), nil
	case "proposal":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		revision, err := req.RequireInt("revision")
		if err != nil || revision < 1 {
			return mcplib.NewToolResultError("revision is required"), nil
		}
		proposal, err := t.svc.GetProposal(ctx, id, revision)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(proposal), nil
	case "proposals":
		id, result := factoryEpicID(req)
		if result != nil {
			return result, nil
		}
		proposals, err := t.svc.ListProposals(ctx, id)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(proposals), nil
	case "pour", "claim_plan", "reopen_issue":
		return mcplib.NewToolResultError("factory action is not permitted"), nil
	}
	return mcplib.NewToolResultError("unknown action"), nil
}

func factoryRecoveryChoices(req mcplib.CallToolRequest) ([]string, error) {
	raw, err := req.RequireString("choices_json")
	if err != nil || raw == "" {
		return []string{}, nil
	}
	var choices []string
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&choices) != nil || decoder.Decode(&struct{}{}) != io.EOF || choices == nil {
		return nil, errors.New("invalid")
	}
	return choices, nil
}

func optionalFormulaID(req mcplib.CallToolRequest) string {
	id, _ := req.RequireString("formula_id")
	return id
}
func factoryEpicID(req mcplib.CallToolRequest) (string, *mcplib.CallToolResult) {
	id, err := req.RequireString("epic_id")
	if err != nil || id == "" {
		return "", mcplib.NewToolResultError("epic_id is required")
	}
	return id, nil
}

func factoryProjectOverrides(req mcplib.CallToolRequest) (map[string]int, error) {
	arguments, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return nil, errors.New("invalid")
	}
	raw, ok := arguments["project_overrides"]
	if !ok {
		return nil, errors.New("missing")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var overrides map[string]int
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&overrides); err != nil || decoder.Decode(&struct{}{}) != io.EOF || overrides == nil {
		return nil, errors.New("invalid")
	}
	return overrides, nil
}
func factoryToolError(err error) *mcplib.CallToolResult {
	if errors.Is(err, factory.ErrActionNotPermitted) {
		return mcplib.NewToolResultError("factory action is not permitted")
	}
	if errors.Is(err, factory.ErrWorkEpicNotFound) {
		return mcplib.NewToolResultError("factory epic not found")
	}
	if errors.Is(err, factory.ErrInstantiationConflict) {
		return mcplib.NewToolResultError("factory instantiation conflict")
	}
	if errors.Is(err, factory.ErrFormulaNotFound) {
		return mcplib.NewToolResultError("factory Formula not found")
	}
	if errors.Is(err, factory.ErrInvalidFormula) {
		return mcplib.NewToolResultError(err.Error())
	}
	return mcplib.NewToolResultError("factory request failed")
}
