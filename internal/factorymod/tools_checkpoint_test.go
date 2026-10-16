package factorymod

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/mark3labs/mcp-go/mcptest"
)

func TestFactoryCheckpointCompletionOmitsPR(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, testTools(svc)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	result := callTool(t, srv, "factory", map[string]any{"action": "complete_attempt", "attempt_id": "attempt-1", "attempt_token": "token", "summary": "Committed and pushed."})
	if result.IsError {
		t.Fatalf("checkpoint completion: %s", resultText(result))
	}
	if svc.completedAttempt != "attempt-1" || svc.completionPRURL != "" {
		t.Fatalf("unexpected handoff: %#v", svc)
	}
}

func (s *fakeFactoryService) RequestCheckpointRecovery(ctx context.Context, attempt, token string) (factory.RecoveryGate, error) {
	return s.CreateRecoveryGate(ctx, attempt, token, "Accept checkpoint?", "Rebased branch", []string{"exact checkpoint"})
}

type checkpointRecoveryService struct {
	stubService
	attempt, token string
	err            error
}

func (s *checkpointRecoveryService) RequestCheckpointRecovery(_ context.Context, attempt, token string) (factory.RecoveryGate, error) {
	s.attempt, s.token = attempt, token
	return factory.RecoveryGate{AttemptID: attempt, Question: "Accept exact checkpoint?"}, s.err
}

func TestFactoryCheckpointRecoveryTool(t *testing.T) {
	svc := &checkpointRecoveryService{}
	wrapped := factoryMCPService{Service: svc}
	srv, err := mcptest.NewServer(t, testTools(wrapped)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	args := map[string]any{"action": "request_checkpoint_recovery", "attempt_id": "attempt", "attempt_token": "token"}
	got := callTool(t, srv, "factory", args)
	if got.IsError || svc.attempt != "attempt" || svc.token != "token" {
		t.Fatalf("recovery did not reach service: %s", resultText(got))
	}
	svc.err = errors.New("checkpoint is not pushed")
	if got := callTool(t, srv, "factory", args); !got.IsError {
		t.Fatal("recovery failure was hidden")
	}
	unsupported := factoryMCPService{Service: struct{ Service }{}}
	if _, err := unsupported.RequestCheckpointRecovery(t.Context(), "attempt", "token"); !errors.Is(err, factory.ErrActionNotPermitted) {
		t.Fatalf("unsupported service: %v", err)
	}
}
