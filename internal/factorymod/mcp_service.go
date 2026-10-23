package factorymod

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/factory"
)

// factoryMCPService is what the factory and factory_control MCP tools call.
// Agents can propose graph edits; approval remains an operator decision, so
// the user-only actions below refuse with ErrActionNotPermitted.
type factoryMCPService struct {
	Service
	proposeControl func(context.Context, string, string, map[string]string, string) (string, error)
}

// ProposeFactoryControl records a control proposal; the user's card runs it.
func (s factoryMCPService) ProposeFactoryControl(ctx context.Context, epicID, action string, args map[string]string, summary string) (string, error) {
	if s.proposeControl == nil {
		return "", factory.ErrActionNotPermitted
	}
	return s.proposeControl(ctx, epicID, action, args, summary)
}

func (s factoryMCPService) CreateWorkEpic(ctx context.Context, req factory.CreateWorkEpicRequest) (factory.WorkEpic, error) {
	if req.FormulaID != "" || req.FormulaRevision != 0 {
		return factory.WorkEpic{}, factory.ErrActionNotPermitted
	}
	return s.Service.CreateWorkEpic(ctx, req)
}

func (s factoryMCPService) MutateGraph(ctx context.Context, mutation factory.GraphMutation) error {
	mutation.Actor = "mcp"
	return s.Service.MutateGraph(ctx, mutation)
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
