package factorymod

import (
	"context"
	"fmt"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/factory"
)

func validateFactoryControlDecision(action string, args map[string]string) error {
	decision := args["decision"]
	switch action {
	case "decide_plan":
		revision, err := strconv.Atoi(args["expected_revision"])
		if err != nil || revision < 1 {
			return fmt.Errorf("%w: expected_revision must be positive", factory.ErrInvalidRequest)
		}
		if decision != "approve" && decision != "revise" && decision != "reject" {
			return fmt.Errorf("%w: decision must be approve, revise, or reject", factory.ErrInvalidRequest)
		}
	case "resolve_authority", "resolve_project":
		if decision != "approve" && decision != "reject" {
			return fmt.Errorf("%w: decision must be approve or reject", factory.ErrInvalidRequest)
		}
		if args["acknowledge_local_execution"] != "" {
			return fmt.Errorf("%w: execution acknowledgement must come from the user confirmation", factory.ErrInvalidRequest)
		}
	}
	return nil
}

func (m *Module) runFactoryControlDecision(ctx context.Context, epicID, action string, args map[string]string, acknowledged ...bool) error {
	if action == "decide_plan" {
		revision, _ := strconv.Atoi(args["expected_revision"])
		_, err := m.svc.DecidePlanGate(ctx, epicID, args["decision"], factory.PlanGateDecisionRequest{ExpectedRevision: revision, ExpectedHash: args["expected_hash"], Actor: "user", Feedback: args["response"], ImplementationModel: args["implementation_model"]})
		return err
	}
	issues, err := m.svc.ListIssues(ctx, epicID)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if action == "resolve_authority" && issue.Authority != nil && issue.Authority.IssueID == args["authority_gate_id"] {
			_, err := m.svc.ResolveAuthorityEscalationGate(ctx, args["authority_gate_id"], args["decision"])
			return err
		}
		if action == "resolve_project" && issue.ProjectRequest != nil && issue.ProjectRequest.IssueID == args["project_gate_id"] {
			consent := len(acknowledged) > 0 && acknowledged[0]
			if args["decision"] == "approve" && !consent {
				return fmt.Errorf("%w: the user must allow local command execution for this project", factory.ErrInvalidRequest)
			}
			resolver, ok := m.svc.(interface {
				ResolveProjectRequest(context.Context, string, string, string, bool) (factory.ProjectRequestGate, error)
			})
			if !ok {
				return factory.ErrFactoryUnavailable
			}
			_, err := resolver.ResolveProjectRequest(ctx, args["project_gate_id"], args["decision"], args["response"], consent)
			return err
		}
	}
	return fmt.Errorf("%w: decision gate is not part of Epic %s", factory.ErrInvalidRequest, epicID)
}
