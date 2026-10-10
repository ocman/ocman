package server

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
)

type scopeFactoryFake struct {
	fakeFactoryService
	proposal                        factory.SubmitProposalRequest
	project, attempt, token, reason string
}

func (s *scopeFactoryFake) SubmitScopePlan(_ context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	s.proposal = req
	return factory.ProposalRevision{EpicID: req.EpicID, Revision: 2}, nil
}

func (s *scopeFactoryFake) RequestProject(_ context.Context, attempt, token, project, reason string) (factory.ProjectRequestGate, error) {
	s.attempt, s.token, s.project, s.reason = attempt, token, project, reason
	return factory.ProjectRequestGate{EpicID: "epic"}, nil
}

func TestFactoryMCPScopeActionsReachUnderlyingService(t *testing.T) {
	underlying := &scopeFactoryFake{}
	service := factoryMCPService{factoryService: underlying}
	req := factory.SubmitProposalRequest{EpicID: "epic", AttemptID: "attempt", AttemptToken: "token"}
	proposal, err := service.SubmitScopePlan(t.Context(), req)
	if err != nil || proposal.Revision != 2 || underlying.proposal.EpicID != req.EpicID || underlying.proposal.AttemptToken != "token" {
		t.Fatalf("scope proposal = %#v, %v", proposal, err)
	}
	gate, err := service.RequestProject(t.Context(), "attempt", "token", "/other", "Missing API")
	if err != nil || gate.EpicID != "epic" || underlying.attempt != "attempt" || underlying.token != "token" || underlying.project != "/other" || underlying.reason != "Missing API" {
		t.Fatalf("project request = %#v, %v", gate, err)
	}
	unsupported := factoryMCPService{factoryService: &fakeFactoryService{}}
	if _, err := unsupported.SubmitScopePlan(t.Context(), req); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported scope = %v", err)
	}
	if _, err := unsupported.RequestProject(t.Context(), "attempt", "token", "/other", "Missing API"); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported project = %v", err)
	}
}

func TestFactoryMCPControlProposalsNeedAProposer(t *testing.T) {
	var got string
	service := factoryMCPService{factoryService: &fakeFactoryService{}, proposeControl: func(_ context.Context, epicID, action string, args map[string]string, summary string) (string, error) {
		got = epicID + ":" + action + ":" + args["issue_id"] + ":" + summary
		return "fca_1", nil
	}}
	if id, err := service.ProposeFactoryControl(t.Context(), "epic", "reopen", map[string]string{"issue_id": "epic.1.3"}, "Retry"); err != nil || id != "fca_1" || got != "epic:reopen:epic.1.3:Retry" {
		t.Fatalf("propose = %q, %v (%q)", id, err, got)
	}
	if _, err := (factoryMCPService{factoryService: &fakeFactoryService{}}).ProposeFactoryControl(t.Context(), "epic", "pause", nil, "Pause"); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("no proposer = %v", err)
	}
}
