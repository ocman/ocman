package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"

	"github.com/NoUseFreak/ocman/internal/loops"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
)

// buildLoopMCPServer builds an mcptest server with the loop tools backed
// by a real loops.Service over an in-memory state DB.
func buildLoopMCPServer(t *testing.T, stateDB *state.DB) *mcptest.Server {
	t.Helper()
	svc := loops.NewService(loops.Deps{
		Store:     stateDB,
		Messenger: noopMessenger{},
	})
	tools := internalmcp.ServerTools(internalmcp.Deps{
		StateDB:     stateDB,
		PlatformID:  "opencode",
		LoopService: svc,
	})
	srv, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

type noopMessenger struct{}

func (noopMessenger) SendPrompt(context.Context, string, string) error { return nil }

func TestCreateLoopTool(t *testing.T) {
	stateDB := openTestStateDB(t)
	srv := buildLoopMCPServer(t, stateDB)

	result := callTool(t, srv, "create_loop", map[string]interface{}{
		"root_session_id": "sess1",
		"title":           "heartbeat loop",
		"trigger_type":    "schedule",
		"trigger_config":  map[string]interface{}{"interval_seconds": 120},
		"action_type":     "prompt_root",
		"action_template": "ping",
		"stop_conditions":  map[string]interface{}{"max_iterations": 10, "max_cost_usd": 2},
	})
	if result.IsError {
		t.Fatalf("create_loop failed: %s", resultText(result))
	}
	text := resultText(result)
	if !strings.Contains(text, "active") || !strings.Contains(text, "heartbeat loop") {
		t.Fatalf("unexpected create_loop result: %q", text)
	}

	// Confirm it persisted.
	got, err := stateDB.ListLoops("sess1", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("expected 1 persisted loop, got %d (err=%v)", len(got), err)
	}
}

func TestCreateLoopTool_RejectsNoBudget(t *testing.T) {
	stateDB := openTestStateDB(t)
	srv := buildLoopMCPServer(t, stateDB)

	result := callTool(t, srv, "create_loop", map[string]interface{}{
		"root_session_id": "sess1",
		"trigger_type":    "schedule",
		"action_type":     "prompt_root",
		"stop_conditions":  map[string]interface{}{"max_iterations": 10},
	})
	if !result.IsError {
		t.Fatalf("expected create_loop to reject missing budget, got: %s", resultText(result))
	}
}

func TestLoopLifecycleTools(t *testing.T) {
	stateDB := openTestStateDB(t)
	srv := buildLoopMCPServer(t, stateDB)

	create := callTool(t, srv, "create_loop", map[string]interface{}{
		"root_session_id": "sess1",
		"trigger_type":    "schedule",
		"trigger_config":  map[string]interface{}{"interval_seconds": 60},
		"action_type":     "prompt_root",
		"stop_conditions":  map[string]interface{}{"max_iterations": 5, "max_cost_usd": 1},
	})
	if create.IsError {
		t.Fatalf("create_loop: %s", resultText(create))
	}
	loopsList, _ := stateDB.ListLoops("sess1", "")
	id := loopsList[0].ID

	for _, tool := range []string{"pause_loop", "resume_loop", "delete_loop"} {
		res := callTool(t, srv, tool, map[string]interface{}{"loop_id": id})
		if res.IsError {
			t.Fatalf("%s: %s", tool, resultText(res))
		}
	}

	status := callTool(t, srv, "get_loop_status", map[string]interface{}{"loop_id": id})
	if status.IsError {
		t.Fatalf("get_loop_status: %s", resultText(status))
	}
	if !strings.Contains(resultText(status), "deleted") {
		t.Fatalf("expected deleted state, got %q", resultText(status))
	}
}
