package mcp_test

import (
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"testing"
)

func TestFactoryCheckpointCompletionOmitsPR(t *testing.T) {
	svc := &fakeFactoryService{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
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
