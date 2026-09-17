package mcp_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/mark3labs/mcp-go/mcptest"
)

type fakeRoutineService struct {
	input routines.Input
	id    string
	err   error
}

func (f *fakeRoutineService) Create(_ context.Context, input routines.Input) (state.Routine, error) {
	f.input = input
	return state.Routine{ID: "routine-1", Name: input.Name}, f.err
}
func (f *fakeRoutineService) Update(_ context.Context, id string, input routines.Input) (state.Routine, error) {
	f.id, f.input = id, input
	return state.Routine{ID: id, Name: input.Name}, f.err
}
func (f *fakeRoutineService) Get(_ context.Context, id string) (state.Routine, error) {
	f.id = id
	return state.Routine{ID: id, Name: "Review"}, f.err
}
func (f *fakeRoutineService) List(context.Context, bool) ([]state.Routine, error) {
	return []state.Routine{{ID: "routine-1", Name: "Review"}}, f.err
}
func (f *fakeRoutineService) Delete(_ context.Context, id string) error {
	f.id = id
	return f.err
}
func (f *fakeRoutineService) RunNow(_ context.Context, id string) (state.RoutineRun, error) {
	f.id = id
	return state.RoutineRun{ID: "run-1", RoutineID: id}, f.err
}
func (f *fakeRoutineService) History(_ context.Context, id string) ([]state.RoutineRun, error) {
	f.id = id
	return []state.RoutineRun{{ID: "run-1", RoutineID: id}}, f.err
}

func routineServer(t *testing.T, svc *fakeRoutineService) *mcptest.Server {
	t.Helper()
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{RoutineService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestRoutineToolActions(t *testing.T) {
	svc := &fakeRoutineService{}
	srv := routineServer(t, svc)

	help := callTool(t, srv, "routines", map[string]any{"action": "help"})
	for _, text := range []string{"create", "update", "delete", "run", "history", "schedule_kind", "output_schema", "directory must be absolute", "Unix timestamp in milliseconds"} {
		if !strings.Contains(resultText(help), text) {
			t.Fatalf("help result missing %q: %s", text, resultText(help))
		}
	}
	if listed := callTool(t, srv, "routines", map[string]any{"action": "list"}); listed.IsError || !strings.Contains(resultText(listed), `"name": "Review"`) {
		t.Fatalf("list result = %q", resultText(listed))
	}

	created := callTool(t, srv, "routines", map[string]any{
		"action": "create", "name": "Review", "prompt": "Review work", "directory": "/repo",
		"session_mode": "reuse", "schedule_kind": "cron", "cron": "0 9 * * 1-5", "timezone": "Europe/Brussels",
		"enabled": true, "delete_after_success": true,
	})
	if created.IsError || svc.input.SessionMode != routines.SessionReuse || svc.input.Schedule.Kind != routines.ScheduleCron || !svc.input.Enabled || !svc.input.DeleteAfterSuccess {
		t.Fatalf("create result = %q, input = %#v", resultText(created), svc.input)
	}

	for _, action := range []string{"get", "run", "history", "delete"} {
		result := callTool(t, srv, "routines", map[string]any{"action": action, "routine_id": "routine-1"})
		if result.IsError || svc.id != "routine-1" {
			t.Fatalf("%s result = %q, id = %q", action, resultText(result), svc.id)
		}
	}

	updated := callTool(t, srv, "routines", map[string]any{"action": "update", "routine_id": "routine-1", "name": "Updated", "prompt": "Prompt", "directory": "/repo"})
	if updated.IsError || svc.id != "routine-1" || svc.input.Name != "Updated" || svc.input.SessionMode != routines.SessionNew || svc.input.Schedule.Kind != routines.ScheduleNone {
		t.Fatalf("update result = %q, input = %#v", resultText(updated), svc.input)
	}
}

func TestRoutineToolValidationAndErrors(t *testing.T) {
	svc := &fakeRoutineService{}
	srv := routineServer(t, svc)

	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{}, want: "action is required"},
		{args: map[string]any{"action": "unknown"}, want: "unknown action"},
		{args: map[string]any{"action": "get"}, want: "routine_id is required"},
		{args: map[string]any{"action": "create", "name": "Review"}, want: "prompt is required"},
		{args: map[string]any{"action": "create", "name": "Review", "prompt": "Prompt", "directory": "/repo", "schedule_kind": "timeout", "timeout_ms": float64(math.MaxInt64/int64(1_000_000) + 1)}, want: "invalid routine"},
		{args: map[string]any{"action": "create", "name": "Review", "prompt": "Prompt", "directory": "/repo", "schedule_kind": "timeout", "timeout_ms": float64(math.MinInt64/int64(1_000_000) - 1)}, want: "invalid routine"},
	} {
		result := callTool(t, srv, "routines", test.args)
		if !result.IsError || resultText(result) != test.want {
			t.Fatalf("args %#v: result = %q, error = %v", test.args, resultText(result), result.IsError)
		}
	}

	svc.err = state.ErrRoutineNotFound
	if result := callTool(t, srv, "routines", map[string]any{"action": "get", "routine_id": "missing"}); !result.IsError || resultText(result) != "routine not found" {
		t.Fatalf("not found result = %q", resultText(result))
	}
	svc.err = errors.New("database details")
	if result := callTool(t, srv, "routines", map[string]any{"action": "list"}); !result.IsError || resultText(result) != "routine request failed" {
		t.Fatalf("internal error result = %q", resultText(result))
	}
}

func TestRoutineToolPermissionRules(t *testing.T) {
	svc := &fakeRoutineService{}
	srv := routineServer(t, svc)

	// Valid permission_rules JSON is parsed and passed through.
	result := callTool(t, srv, "routines", map[string]any{
		"action": "create", "name": "Guarded run", "prompt": "go", "directory": "/repo",
		"permission_rules": `[{"permission":"bash","pattern":"*","action":"allow"},{"permission":"edit","pattern":"src/*","action":"ask"}]`,
	})
	if result.IsError {
		t.Fatalf("create with permission_rules failed: %s", resultText(result))
	}
	want := []platforms.PermissionRule{
		{Permission: "bash", Pattern: "*", Action: "allow"},
		{Permission: "edit", Pattern: "src/*", Action: "ask"},
	}
	if len(svc.input.PermissionRules) != len(want) {
		t.Fatalf("PermissionRules = %v, want %v", svc.input.PermissionRules, want)
	}
	for i, r := range want {
		if svc.input.PermissionRules[i] != r {
			t.Errorf("rule[%d] = %+v, want %+v", i, svc.input.PermissionRules[i], r)
		}
	}

	// Empty array is valid (clears rules / uses platform defaults).
	result = callTool(t, srv, "routines", map[string]any{
		"action": "create", "name": "Default perms", "prompt": "go", "directory": "/repo",
		"permission_rules": `[]`,
	})
	if result.IsError || len(svc.input.PermissionRules) != 0 {
		t.Fatalf("empty rules: result=%q rules=%v", resultText(result), svc.input.PermissionRules)
	}

	// Omitting permission_rules entirely leaves it nil/empty.
	result = callTool(t, srv, "routines", map[string]any{
		"action": "create", "name": "No perms", "prompt": "go", "directory": "/repo",
	})
	if result.IsError || svc.input.PermissionRules != nil {
		t.Fatalf("omitted rules: result=%q rules=%v", resultText(result), svc.input.PermissionRules)
	}

	// Invalid JSON is rejected with a validation error.
	result = callTool(t, srv, "routines", map[string]any{
		"action": "create", "name": "Bad perms", "prompt": "go", "directory": "/repo",
		"permission_rules": `not-json`,
	})
	if !result.IsError {
		t.Fatalf("invalid JSON: expected error, got %q", resultText(result))
	}
}
