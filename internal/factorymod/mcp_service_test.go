package factorymod

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
)

// stubService records what the MCP wrapper forwards. Unimplemented methods
// panic through the nil embedded Service, so a refusal that forwards fails.
type stubService struct {
	Service
	proposal                        factory.SubmitProposalRequest
	submitProposalReq               factory.SubmitProposalRequest
	mutation                        factory.GraphMutation
	gateAction                      string
	project, attempt, token, reason string
}

func (s *stubService) SubmitScopePlan(_ context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	s.proposal = req
	return factory.ProposalRevision{EpicID: req.EpicID, Revision: 2}, nil
}

func (s *stubService) RequestProject(_ context.Context, attempt, token, project, reason string) (factory.ProjectRequestGate, error) {
	s.attempt, s.token, s.project, s.reason = attempt, token, project, reason
	return factory.ProjectRequestGate{EpicID: "epic"}, nil
}

func (s *stubService) SubmitProposal(_ context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	s.submitProposalReq = req
	return factory.ProposalRevision{}, nil
}

func (s *stubService) MutateGraph(_ context.Context, mutation factory.GraphMutation) error {
	s.mutation = mutation
	return nil
}

func TestFactoryMCPScopeActionsReachUnderlyingService(t *testing.T) {
	underlying := &stubService{}
	service := factoryMCPService{Service: underlying}
	req := factory.SubmitProposalRequest{EpicID: "epic", AttemptID: "attempt", AttemptToken: "token"}
	proposal, err := service.SubmitScopePlan(t.Context(), req)
	if err != nil || proposal.Revision != 2 || underlying.proposal.EpicID != req.EpicID || underlying.proposal.AttemptToken != "token" {
		t.Fatalf("scope proposal = %#v, %v", proposal, err)
	}
	gate, err := service.RequestProject(t.Context(), "attempt", "token", "/other", "Missing API")
	if err != nil || gate.EpicID != "epic" || underlying.attempt != "attempt" || underlying.token != "token" || underlying.project != "/other" || underlying.reason != "Missing API" {
		t.Fatalf("project request = %#v, %v", gate, err)
	}
	unsupported := factoryMCPService{Service: struct{ Service }{}}
	if _, err := unsupported.SubmitScopePlan(t.Context(), req); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported scope = %v", err)
	}
	if _, err := unsupported.RequestProject(t.Context(), "attempt", "token", "/other", "Missing API"); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported project = %v", err)
	}
}

func TestFactoryMCPControlProposalsNeedAProposer(t *testing.T) {
	var got string
	service := factoryMCPService{Service: &stubService{}, proposeControl: func(_ context.Context, epicID, action string, args map[string]string, summary string) (string, error) {
		got = epicID + ":" + action + ":" + args["issue_id"] + ":" + summary
		return "fca_1", nil
	}}
	if id, err := service.ProposeFactoryControl(t.Context(), "epic", "reopen", map[string]string{"issue_id": "epic.1.3"}, "Retry"); err != nil || id != "fca_1" || got != "epic:reopen:epic.1.3:Retry" {
		t.Fatalf("propose = %q, %v (%q)", id, err, got)
	}
	if _, err := (factoryMCPService{Service: &stubService{}}).ProposeFactoryControl(t.Context(), "epic", "pause", nil, "Pause"); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("no proposer = %v", err)
	}
}

func TestDedicatedMCPFactoryServiceRejectsUserOnlyActions(t *testing.T) {
	service := factoryMCPService{Service: &stubService{}}
	checks := []struct {
		name string
		call func() error
	}{
		{"create Epic with custom Formula", func() error {
			_, err := service.CreateWorkEpic(t.Context(), factory.CreateWorkEpicRequest{FormulaID: "custom/team", FormulaRevision: 1})
			return err
		}},
		{"create Epic with Formula revision", func() error {
			_, err := service.CreateWorkEpic(t.Context(), factory.CreateWorkEpicRequest{FormulaRevision: 1})
			return err
		}},
		{"save Formula", func() error { _, err := service.SaveFormula(t.Context(), factory.FormulaSaveRequest{}); return err }},
		{"set capacity", func() error { _, err := service.SetCapacityPolicy(t.Context(), factory.CapacityPolicy{}); return err }},
		{"decide Plan", func() error {
			_, err := service.DecidePlanGate(t.Context(), "epic", "approve", factory.PlanGateDecisionRequest{})
			return err
		}},
		{"resolve recovery", func() error { _, err := service.ResolveRecoveryGate(t.Context(), "gate", "resume", ""); return err }},
		{"resolve authority", func() error {
			_, err := service.ResolveAuthorityEscalationGate(t.Context(), "gate", "approve")
			return err
		}},
		{"resolve project request", func() error {
			_, err := service.ResolveProjectRequest(t.Context(), "gate", "approve", "", true)
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, factory.ErrActionNotPermitted) {
				t.Fatalf("error = %v, want ErrActionNotPermitted", err)
			}
		})
	}
}

func TestMCPFactoryImportsPlanButCannotApproveIt(t *testing.T) {
	underlying := &stubService{}
	service := factoryMCPService{Service: underlying}
	if _, err := service.SubmitProposal(t.Context(), factory.SubmitProposalRequest{Import: true, EpicID: "epic"}); err != nil || !underlying.submitProposalReq.Import {
		t.Fatalf("import = %#v, %v", underlying.submitProposalReq, err)
	}
	if _, err := service.DecidePlanGate(t.Context(), "epic", "approve", factory.PlanGateDecisionRequest{ExpectedRevision: 1, ExpectedHash: "hash"}); !errors.Is(err, factory.ErrActionNotPermitted) || underlying.gateAction != "" {
		t.Fatalf("agent approved imported plan: %v", err)
	}
}

func TestDedicatedMCPFactoryServiceAllowsGraphMutations(t *testing.T) {
	underlying := &stubService{}
	service := factoryMCPService{Service: underlying}
	for _, action := range []string{"create", "edit", "reparent", "link", "unlink", "delete"} {
		mutation := factory.GraphMutation{Action: action, EpicID: "epic", ParentID: "epic.1", Kind: "mol", Title: "Work", Actor: "mcp"}
		if err := service.MutateGraph(t.Context(), mutation); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if !reflect.DeepEqual(underlying.mutation, mutation) {
			t.Fatalf("mutation = %+v, want %+v", underlying.mutation, mutation)
		}
	}
	for _, kind := range []string{"implementation", "task"} {
		if err := service.MutateGraph(t.Context(), factory.GraphMutation{Action: "create", Kind: kind, Actor: "user"}); err != nil || underlying.mutation.Actor != "mcp" {
			t.Fatalf("%s create = %#v, %v, want agent proposal", kind, underlying.mutation, err)
		}
	}
}
