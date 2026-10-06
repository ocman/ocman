package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/factory"
)

// Agents can propose graph edits; approval remains an operator decision.
type factoryMCPService struct {
	factoryService
	consumeUnblock func(string, string) bool
}

func (s factoryMCPService) ConsumeFactoryUnblock(token, epicID string) bool {
	return s.consumeUnblock != nil && s.consumeUnblock(token, epicID)
}

func (s factoryMCPService) CreateWorkEpic(ctx context.Context, req factory.CreateWorkEpicRequest) (factory.WorkEpic, error) {
	if req.FormulaID != "" || req.FormulaRevision != 0 {
		return factory.WorkEpic{}, factory.ErrActionNotPermitted
	}
	return s.factoryService.CreateWorkEpic(ctx, req)
}

func (s factoryMCPService) MutateGraph(ctx context.Context, mutation factory.GraphMutation) error {
	mutation.Actor = "mcp"
	return s.factoryService.MutateGraph(ctx, mutation)
}

func (s factoryMCPService) SubmitScopePlan(ctx context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	service, ok := s.factoryService.(interface {
		SubmitScopePlan(context.Context, factory.SubmitProposalRequest) (factory.ProposalRevision, error)
	})
	if !ok {
		return factory.ProposalRevision{}, factory.ErrActionNotPermitted
	}
	return service.SubmitScopePlan(ctx, req)
}

func (s factoryMCPService) RequestProject(ctx context.Context, attemptID, token, project, reason string) (factory.ProjectRequestGate, error) {
	service, ok := s.factoryService.(interface {
		RequestProject(context.Context, string, string, string, string) (factory.ProjectRequestGate, error)
	})
	if !ok {
		return factory.ProjectRequestGate{}, factory.ErrActionNotPermitted
	}
	return service.RequestProject(ctx, attemptID, token, project, reason)
}

func (s factoryMCPService) ReopenIssue(ctx context.Context, epicID, issueID string) error {
	reopener, ok := s.factoryService.(interface {
		ReopenIssue(context.Context, string, string) error
	})
	if !ok {
		return factory.ErrActionNotPermitted
	}
	return reopener.ReopenIssue(ctx, epicID, issueID)
}

func (s factoryMCPService) MutateFactoryUnblock(ctx context.Context, mutation factory.GraphMutation) error {
	return s.factoryService.MutateGraph(ctx, mutation)
}

func (factoryMCPService) SaveFormula(context.Context, factory.FormulaSaveRequest) (factory.NativeFormulaView, error) {
	return factory.NativeFormulaView{}, factory.ErrActionNotPermitted
}

func (factoryMCPService) SetCapacityPolicy(context.Context, factory.CapacityPolicy) (factory.CapacityPolicy, error) {
	return factory.CapacityPolicy{}, factory.ErrActionNotPermitted
}

func (factoryMCPService) DecidePlanGate(context.Context, string, string, factory.PlanGateDecisionRequest) (factory.PlanGate, error) {
	return factory.PlanGate{}, factory.ErrActionNotPermitted
}

func (factoryMCPService) ResolveRecoveryGate(context.Context, string, string, string) (factory.RecoveryGate, error) {
	return factory.RecoveryGate{}, factory.ErrActionNotPermitted
}

func (factoryMCPService) ResolveAuthorityEscalationGate(context.Context, string, string) (factory.AuthorityEscalationGate, error) {
	return factory.AuthorityEscalationGate{}, factory.ErrActionNotPermitted
}

func (factoryMCPService) ResolveProjectRequest(context.Context, string, string, string, bool) (factory.ProjectRequestGate, error) {
	return factory.ProjectRequestGate{}, factory.ErrActionNotPermitted
}
