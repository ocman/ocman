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

func (s *scopeFactoryFake) AddVerificationFollowUps(_ context.Context, attempt, token string, _ factory.GraphMutation) ([]string, error) {
	s.attempt, s.token = attempt, token
	return []string{"epic.1.3.1"}, nil
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
	if created, err := service.AddVerificationFollowUps(t.Context(), "verify", "vtoken", factory.GraphMutation{}); err != nil || len(created) != 1 || underlying.attempt != "verify" || underlying.token != "vtoken" {
		t.Fatalf("follow-ups = %v, %v", created, err)
	}
	unsupported := factoryMCPService{factoryService: &fakeFactoryService{}}
	if _, err := unsupported.AddVerificationFollowUps(t.Context(), "a", "t", factory.GraphMutation{}); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported follow-ups = %v", err)
	}
	if _, err := unsupported.SubmitScopePlan(t.Context(), req); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported scope = %v", err)
	}
	if _, err := unsupported.RequestProject(t.Context(), "attempt", "token", "/other", "Missing API"); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported project = %v", err)
	}
}
