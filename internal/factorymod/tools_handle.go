package factorymod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/platforms"
)

func (t *factoryTools) handleAction(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
		var projects []factory.ProjectAdmission
		if raw, present := arguments["projects"]; present {
			encoded, marshalErr := json.Marshal(raw)
			if marshalErr != nil || json.Unmarshal(encoded, &projects) != nil {
				return mcplib.NewToolResultError("projects is invalid"), nil
			}
		}
		var permRules []platforms.PermissionRule
		if raw := req.GetString("permission_rules", ""); raw != "" {
			if err := json.Unmarshal([]byte(raw), &permRules); err != nil {
				return mcplib.NewToolResultError("permission_rules: invalid JSON"), nil
			}
		}
		epic, err := t.svc.CreateWorkEpic(ctx, factory.CreateWorkEpicRequest{
			Platform:                  req.GetString("platform", ""),
			InstantiationID:           req.GetString("instantiation_id", ""),
			EpicID:                    req.GetString("epic_id", ""),
			Goal:                      req.GetString("goal", ""),
			Brief:                     req.GetString("brief", ""),
			InitialProject:            req.GetString("initial_project", ""),
			FormulaID:                 req.GetString("formula_id", ""),
			FormulaRevision:           req.GetInt("revision", 0),
			AcknowledgeLocalExecution: true,
			Projects:                  projects,
			PermissionRules:           permRules,
		})
		if err != nil {
			return factoryToolError(err), nil
		}
		result := toolResultJSON(epic)
		result.Content = append(result.Content, mcplib.NewTextContent("Ocman shows the user the created epic card under this tool call: "+factoryCardMarker(epic.ID, "", "created")+". "+factoryCardNoCopy))
		return result, nil
	case "complete_attempt":
		attemptID, _ := req.RequireString("attempt_id")
		token, _ := req.RequireString("attempt_token")
		summary, _ := req.RequireString("summary")
		prURL := req.GetString("pr_url", "")
		if err := t.svc.CompleteAttempt(ctx, attemptID, token, summary, prURL); errors.Is(err, factory.ErrVerificationChecksPending) {
			return toolResultJSON(map[string]string{"status": "checks_running", "message": "Factory is running this step's Formula checks. The results will arrive in this session as a new message; end your turn now and act on them when they arrive."}), nil
		} else if err != nil {
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
	case "request_checkpoint_recovery":
		gate, err := t.svc.RequestCheckpointRecovery(ctx, req.GetString("attempt_id", ""), req.GetString("attempt_token", ""))
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
	case "request_project":
		gate, err := t.svc.RequestProject(ctx, req.GetString("attempt_id", ""), req.GetString("attempt_token", ""), req.GetString("project", ""), req.GetString("reason", ""))
		if err != nil {
			return factoryToolError(err), nil
		}
		result := toolResultJSON(gate)
		result.Content = append(result.Content, mcplib.NewTextContent("The project request needs a human decision: "+factoryCardMarker(gate.EpicID, gate.IssueID, "request_project")+". "+factoryCardHandoff))
		return result, nil
	case "submit_scope_plan", "mutate_graph":
		return t.handleGraphAction(ctx, req)
	case "resume_recovery", "retry_recovery", "cancel_recovery":
		gateID, _ := req.RequireString("recovery_gate_id")
		response, _ := req.RequireString("response")
		gate, err := t.svc.ResolveRecoveryGate(ctx, gateID, strings.TrimSuffix(action, "_recovery"), response)
		if err != nil {
			return factoryToolError(err), nil
		}
		return toolResultJSON(gate), nil
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
		implementationModel, _ := req.RequireString("implementation_model")
		gate, err := t.svc.DecidePlanGate(ctx, id, strings.TrimSuffix(action, "_plan"), factory.PlanGateDecisionRequest{ExpectedRevision: revision, ExpectedHash: hash, Feedback: feedback, ImplementationModel: implementationModel})
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
	case "submit_proposal", "import_proposal":
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
		imported := action == "import_proposal"
		if imported && (attemptID != "" || token != "") {
			return mcplib.NewToolResultError("import_proposal does not accept attempt credentials"), nil
		}
		if !imported && (attemptID == "" || token == "") {
			return mcplib.NewToolResultError("attempt_id and attempt_token are required"), nil
		}
		proposal, err := t.svc.SubmitProposal(ctx, factory.SubmitProposalRequest{EpicID: id, Manifest: manifest, RationaleMarkdown: rationale, AttemptID: attemptID, AttemptToken: token, Import: imported})
		if err != nil {
			return factoryToolError(err), nil
		}
		result = toolResultJSON(proposal)
		if imported {
			result.Content = append(result.Content, mcplib.NewTextContent("The imported plan awaits human approval: "+factoryCardMarker(id, "", "approve_plan")+". Do not approve or start implementation on the user's behalf. "+factoryCardHandoff))
		}
		return result, nil
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
	case "claim_plan", "reopen_issue":
		return mcplib.NewToolResultError("factory action is not permitted"), nil
	}
	return mcplib.NewToolResultError("unknown action"), nil
}
