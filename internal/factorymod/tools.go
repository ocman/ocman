package factorymod

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/NoUseFreak/ocman/internal/factory"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type toolService interface {
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
type factoryTools struct{ svc toolService }

const factoryFormulaExample = `version = 1\nname = \"Team\"\n\n[[input]]\nkey = \"goal\"\n\n[[input]]\nkey = \"initial_project\"\n\n[[issue]]\nkey = \"plan\"\nkind = \"plan\"\n`

const factoryReplyCards = "Results that create an epic or need a human action carry a [[ocman:card ...]] marker. Ocman shows that card to the user under the tool call and hides it once the action is resolved. When a card asks for a human action, tell the user a card is waiting for them and that they must handle it there. Never copy, invent, or repeat card markers in your replies."

// Ocman renders cards from the tool result, so agents explain them instead of copying markers.
const (
	factoryCardNoCopy  = "Do not copy the marker into your reply."
	factoryCardHandoff = "Ocman now shows the user a card for this action under this tool call. Tell the user a card is waiting for them and that they must handle it there; you cannot do it for them. " + factoryCardNoCopy
)

type factoryAction struct {
	name, description, example string
	required, optional         []string
	output                     any
	errors                     []string
}

var factoryFormulaOutput = map[string]string{"id": "string", "version": "integer", "name": "string", "source": "string", "hash": "string (canonical compiled JSON)", "sourceHash": "string (TOML source provenance)", "compiled": "object (canonical interchange JSON)", "inputs": "string[]", "nodes": "FormulaGraphNode[]", "edges": "FormulaGraphEdge[]", "composition": "FormulaComposition[]", "valid": "boolean", "errors": "string[]"}

var factoryActions = []factoryAction{
	{name: "import_proposal", description: "Imports a complete existing plan from a plain session without launching a planning session. First create the Epic, then use issues to find its root mol ID. Pass the same manifest format as submit_proposal, with self-contained ticket descriptions and dependency edges. Only allowed before any Factory attempt has been claimed and while the approval gate is unresolved. Re-import to submit a revised plan. Returns an immutable proposal awaiting human approval; never starts implementation. Present the proposal for review and wait for explicit approval and implementation-model confirmation.", example: `{"action":"import_proposal","epic_id":"epic-1","manifest_json":"{\"epicId\":\"epic-1\",\"molId\":\"epic-1\",\"project\":\"/repo\",\"nodes\":[{\"key\":\"implement\",\"type\":\"implementation\",\"requirement\":\"required\",\"title\":\"Implement the change\",\"description\":\"Scope and verification steps.\",\"acceptanceCriteria\":[\"A verifiable outcome\"]}]}"}`, required: []string{"epic_id", "manifest_json"}, optional: []string{"rationale_markdown"}, output: "ProposalRevision", errors: []string{"epic_id is required", "manifest_json is required", "manifest_json is invalid", "import_proposal does not accept attempt credentials", "factory action is not permitted", "factory epic not found", "factory request failed"}},
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
	{name: "submit_proposal", description: "Creates the next immutable issue-graph proposal revision. Define implementation Issues and optional Project Delivery placeholders as nodes. Dependencies point from dependent to blocker and use blocks, on_failure, or merge_gated. A merge_gated edge must target another project's delivery node. Legacy per-node dependsOn keys are blocks. At least one node must be a required implementation and dependencies must be acyclic. Every non-reference implementation node needs acceptanceCriteria: 1–20 verifiable outcomes the validator will check.", example: `{"action":"submit_proposal","epic_id":"epic-1","manifest_json":"{\"epicId\":\"epic-1\",\"molId\":\"epic-1\",\"project\":\"/repo\",\"nodes\":[{\"key\":\"backend\",\"type\":\"implementation\",\"requirement\":\"required\",\"title\":\"Build backend\",\"description\":\"Add the API.\",\"acceptanceCriteria\":[\"GET /api/items returns 200 with the stored items\"]},{\"key\":\"frontend\",\"type\":\"implementation\",\"requirement\":\"required\",\"title\":\"Build frontend\",\"description\":\"Add the UI.\",\"acceptanceCriteria\":[\"The items page lists every item from the API\"]}],\"edges\":[{\"from\":\"frontend\",\"to\":\"backend\",\"type\":\"blocks\"}]}","attempt_id":"fa_1","attempt_token":"fat_1"}`, required: []string{"epic_id", "manifest_json", "attempt_id", "attempt_token"}, optional: []string{"rationale_markdown"}, output: "ProposalRevision", errors: []string{"epic_id is required", "manifest_json is required", "attempt_id and attempt_token are required", "factory action is not permitted", "manifest_json is invalid", "factory epic not found", "factory request failed"}},
	{name: "proposals", description: "Lists immutable proposal revisions for a Work Epic.", example: `{"action":"proposals","epic_id":"epic-1"}`, required: []string{"epic_id"}, output: "ProposalRevision[]", errors: []string{"epic_id is required", "factory epic not found", "factory request failed"}},
	{name: "proposal", description: "Gets one immutable proposal revision.", example: `{"action":"proposal","epic_id":"epic-1","revision":1}`, required: []string{"epic_id", "revision"}, output: "ProposalRevision", errors: []string{"epic_id is required", "revision is required", "factory epic not found", "factory request failed"}},
	{name: "approve_plan", description: "Approves the exact proposed plan revision and saves the implementation model. Ask the user to confirm the model; recommend Opus/Sol for balanced work or Sonnet/Terra for speed.", example: `{"action":"approve_plan","epic_id":"epic-1","revision":1,"expected_hash":"hash","implementation_model":"openai/gpt-5.6-sol"}`, required: []string{"epic_id", "revision", "expected_hash"}, optional: []string{"feedback", "implementation_model"}, output: "PlanGate", errors: []string{"epic_id is required", "revision is required", "expected_hash is required", "factory request failed"}},
	{name: "revise_plan", description: "Requests revision of the exact proposed plan.", example: `{"action":"revise_plan","epic_id":"epic-1","revision":1,"expected_hash":"hash","feedback":"Clarify scope"}`, required: []string{"epic_id", "revision", "expected_hash"}, optional: []string{"feedback"}, output: "PlanGate", errors: []string{"epic_id is required", "revision is required", "expected_hash is required", "factory request failed"}},
	{name: "reject_plan", description: "Rejects the exact proposed plan.", example: `{"action":"reject_plan","epic_id":"epic-1","revision":1,"expected_hash":"hash"}`, required: []string{"epic_id", "revision", "expected_hash"}, optional: []string{"feedback"}, output: "PlanGate", errors: []string{"epic_id is required", "revision is required", "expected_hash is required", "factory request failed"}},
	{name: "get_capacity_policy", description: "Gets Factory capacity limits.", example: `{"action":"get_capacity_policy"}`, output: "CapacityPolicy", errors: []string{"factory request failed"}},
	{name: "set_capacity_policy", description: "Sets Factory capacity limits.", example: `{"action":"set_capacity_policy","global_capacity":10,"project_capacity":4,"project_overrides":{"/repo":2}}`, required: []string{"global_capacity", "project_capacity", "project_overrides"}, output: "CapacityPolicy", errors: []string{"global_capacity is required", "project_capacity is required", "project_overrides is invalid", "factory request failed"}},
	{name: "request_recovery", description: "Pauses the assigned implementation Attempt for a human decision while preserving its session and worktree.", example: `{"action":"request_recovery","attempt_id":"fa-1","attempt_token":"token","question":"Which API should I use?","reason":"Both supported APIs change persisted behavior.","choices_json":"[\"API A\",\"API B\"]"}`, required: []string{"attempt_id", "attempt_token", "question", "reason"}, optional: []string{"choices_json"}, output: "RecoveryGate", errors: []string{"attempt_id is required", "attempt_token is required", "question is required", "reason is required", "choices_json is invalid", "factory request failed"}},
	{name: "request_project", description: "Pauses the assigned implementation Attempt and asks a human to admit one additional local Git project.", example: `{"action":"request_project","attempt_id":"fa-1","attempt_token":"token","project":"/repo","reason":"The shared contract must change."}`, required: []string{"attempt_id", "attempt_token", "project", "reason"}, output: "ProjectRequestGate", errors: []string{"attempt_id is required", "attempt_token is required", "project is required", "reason is required", "factory action is not permitted", "factory request failed"}},
	{name: "submit_scope_plan", description: "Appends required work for an approved project expansion without replacing completed Issues or checkpoints. The revised graph requires separate human approval before new work can run.", example: `{"action":"submit_scope_plan","epic_id":"epic-1","manifest_json":"{\"epicId\":\"epic-1\",\"molId\":\"epic-1.1\",\"project\":\"/repo\",\"nodes\":[{\"key\":\"shared\",\"type\":\"implementation\",\"requirement\":\"required\",\"project\":\"/other\"}]}","attempt_id":"fa-1","attempt_token":"token"}`, required: []string{"epic_id", "manifest_json", "attempt_id", "attempt_token"}, optional: []string{"rationale_markdown"}, output: "ProposalRevision", errors: []string{"factory action is not permitted", "manifest_json is invalid", "factory request failed"}},
	{name: "request_checkpoint_recovery", description: "Requests human approval of the exact clean, pushed HEAD after an authorized rebase. The generated recovery choice binds the attempt, repository, branch, original checkpoint and new SHA. Only a human resume decision can authorize it; a changed HEAD needs a new decision.", example: `{"action":"request_checkpoint_recovery","attempt_id":"fa-1","attempt_token":"token"}`, required: []string{"attempt_id", "attempt_token"}, output: "RecoveryGate", errors: []string{"attempt_id is required", "attempt_token is required", "factory request failed"}},
	{name: "complete_attempt", description: "Records a verified clean, pushed commit checkpoint for an implementation Attempt. Omit pr_url during implementation. Only the final delivery Attempt requires pr_url, after creating or reusing the Epic's final review-ready pull request.", example: `{"action":"complete_attempt","attempt_id":"fa-1","attempt_token":"token","summary":"Implemented and tested."}`, required: []string{"attempt_id", "attempt_token", "summary"}, optional: []string{"pr_url"}, output: map[string]string{"status": "completed"}, errors: []string{"attempt_id is required", "attempt_token is required", "summary is required", "factory request failed"}},
	{name: "resume_recovery", description: "Resumes the paused implementation session with a durable human response.", example: `{"action":"resume_recovery","recovery_gate_id":"gate-1","response":"Use API A."}`, required: []string{"recovery_gate_id"}, optional: []string{"response"}, output: "RecoveryGate", errors: []string{"recovery_gate_id is required", "factory request failed"}},
	{name: "retry_recovery", description: "Ends the paused attempt and starts a fresh implementation Attempt.", example: `{"action":"retry_recovery","recovery_gate_id":"gate-1","response":"Retry from a clean worktree."}`, required: []string{"recovery_gate_id"}, optional: []string{"response"}, output: "RecoveryGate", errors: []string{"recovery_gate_id is required", "factory request failed"}},
	{name: "cancel_recovery", description: "Cancels the paused implementation Issue.", example: `{"action":"cancel_recovery","recovery_gate_id":"gate-1","response":"No longer needed."}`, required: []string{"recovery_gate_id"}, optional: []string{"response"}, output: "RecoveryGate", errors: []string{"recovery_gate_id is required", "factory request failed"}},
	{name: "approve_authority", description: "Approves one out-of-profile permission request exactly once.", example: `{"action":"approve_authority","authority_gate_id":"gate-1"}`, required: []string{"authority_gate_id"}, output: "AuthorityEscalationGate", errors: []string{"authority_gate_id is required", "factory request failed"}},
	{name: "reject_authority", description: "Rejects one out-of-profile permission request exactly once.", example: `{"action":"reject_authority","authority_gate_id":"gate-1"}`, required: []string{"authority_gate_id"}, output: "AuthorityEscalationGate", errors: []string{"authority_gate_id is required", "factory request failed"}},
	factoryGraphAction,
	{name: "create", description: "Creates and pours a Factory Work Epic with the built-in tracer Formula. For an already-planned ticket breakdown, use issues then import_proposal to skip the planning session while keeping human approval. goal is the Epic's title: one short clear line of at most 80 characters, e.g. \"Prettify Factory Epic IDs\" — never a paragraph. Put context, constraints and decisions in brief instead. Always pass epic_id: a short human-friendly kebab-case name for the work (2-40 lowercase letters, digits and dashes), e.g. pretty-epic-ids. If it comes back taken, call create again with a different name.", example: `{"action":"create","epic_id":"pretty-epic-ids","goal":"Prettify Factory Epic IDs","brief":"IDs are built from initials today.","initial_project":"/repo","acknowledge_local_execution":true,"projects":[{"path":"/other","acknowledgeLocalExecution":true}]}`, required: []string{"goal", "initial_project", "acknowledge_local_execution"}, optional: []string{"platform", "epic_id", "brief", "instantiation_id", "projects"}, output: "WorkEpic", errors: []string{"goal is required", "initial_project is required", "acknowledge_local_execution must be true", "goal must be a short clear title of at most 80 characters; move the detail into brief", "factory epic id already taken: pick another human-friendly id", "factory action is not permitted", "factory request failed"}},
	{name: "claim_plan", description: "Claiming Factory Planning Work is a human action. Ocman shows the user a card to handle it; tell them it is waiting and do not copy its marker.", example: `{"action":"claim_plan","epic_id":"epic-1","issue_id":"epic-1.1"}`, optional: []string{"epic_id", "issue_id"}, output: "Denial with a human action card marker", errors: []string{"factory action is not permitted"}},
	{name: "reopen_issue", description: "Reopening failed or cancelled work is a human action. Pass epic_id and issue_id, then tell the user the Reopen issue card Ocman shows is waiting for them; do not copy its marker. Do not retry the denied action.", example: `{"action":"reopen_issue","epic_id":"epic-1","issue_id":"epic-1.3"}`, optional: []string{"epic_id", "issue_id"}, output: "Denial with a human action card marker", errors: []string{"factory action is not permitted"}},
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
	help["reply_cards"] = factoryReplyCards
	help["errors"] = []string{"action is required", "unknown action"}
	return help
}

func factoryServerTools(tools *factoryTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("factory", mcplib.WithString("platform"), mcplib.WithDescription("Factory Planning Work actions."), mcplib.WithString("implementation_model"), mcplib.WithString("action", mcplib.Required()), mcplib.WithString("epic_id"), mcplib.WithString("issue_id"), mcplib.WithString("body"), mcplib.WithString("goal"), mcplib.WithString("brief"), mcplib.WithString("initial_project"), mcplib.WithString("project"), mcplib.WithString("instantiation_id"), mcplib.WithBoolean("acknowledge_local_execution"), mcplib.WithArray("projects", mcplib.Items(map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "remoteId": map[string]any{"type": "string"}, "acknowledgeLocalExecution": map[string]any{"type": "boolean"}}, "required": []string{"path", "acknowledgeLocalExecution"}})), mcplib.WithString("formula_id"), mcplib.WithString("formula_source"), mcplib.WithString("manifest_json"), mcplib.WithString("mutation_json"), mcplib.WithString("rationale_markdown"), mcplib.WithString("expected_hash"), mcplib.WithString("feedback"), mcplib.WithString("attempt_id"), mcplib.WithString("attempt_token"), mcplib.WithString("summary"), mcplib.WithString("pr_url"), mcplib.WithString("recovery_gate_id"), mcplib.WithString("authority_gate_id"), mcplib.WithString("question"), mcplib.WithString("reason"), mcplib.WithString("choices_json"), mcplib.WithString("response"), mcplib.WithNumber("revision"), mcplib.WithNumber("global_capacity"), mcplib.WithNumber("project_capacity"), mcplib.WithObject("project_overrides"), mcplib.WithString("permission_rules")), Handler: tools.handle}}
}
func (t *factoryTools) handle(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	result, err := t.handleAction(ctx, req)
	return factoryHumanActionResult(ctx, t.svc, req, result), err
}

// Denials stay errors; the marker only renders controls for a human to click.
func factoryHumanActionResult(ctx context.Context, svc toolService, req mcplib.CallToolRequest, result *mcplib.CallToolResult) *mcplib.CallToolResult {
	if result == nil || !result.IsError || len(result.Content) == 0 {
		return result
	}
	text, ok := result.Content[0].(mcplib.TextContent)
	if !ok || text.Text != "factory action is not permitted" {
		return result
	}
	epicID, issueID := req.GetString("epic_id", ""), req.GetString("issue_id", "")
	if issueID == "" {
		issueID = req.GetString("recovery_gate_id", req.GetString("authority_gate_id", ""))
	}
	var mutation factory.GraphMutation
	if json.Unmarshal([]byte(req.GetString("mutation_json", "")), &mutation) == nil {
		if epicID == "" {
			epicID = mutation.EpicID
		}
		if issueID == "" {
			issueID = mutation.IssueID
		}
	}
	// Gate decisions name only their Issue. Resolve its owner for the card.
	if epicID == "" && issueID != "" && svc != nil {
		epics, _ := svc.ListWorkEpics(ctx)
		for _, epic := range epics {
			issues, _ := svc.ListIssues(ctx, epic.ID)
			for _, issue := range issues {
				if issue.ID == issueID {
					epicID = epic.ID
					break
				}
			}
			if epicID != "" {
				break
			}
		}
	}
	action := req.GetString("action", "")
	if action == "create" {
		epicID, issueID = "", ""
	}
	result.Content = append(result.Content, mcplib.NewTextContent("This action needs a human: "+factoryCardMarker(epicID, issueID, action)+". Do not retry the denied action. "+factoryCardHandoff))
	return result
}

func factoryCardMarker(epicID, issueID, action string) string {
	target := "type=factory-epic"
	if epicID != "" {
		if issueID != "" {
			target = "type=factory-issue"
		}
		target += " epic=" + url.PathEscape(epicID)
		if issueID != "" {
			target += " issue=" + url.PathEscape(issueID)
		}
	}
	return "[[ocman:card " + target + " action=" + action + "]]"
}

var toolResultJSON = internalmcp.ToolResultJSON
