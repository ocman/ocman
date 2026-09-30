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
	input  routines.Input
	id     string
	err    error
	stored *state.Routine
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
	if f.stored != nil {
		return *f.stored, f.err
	}
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
		"enabled": true, "delete_after_success": true, "archive_session_after_success": true,
	})
	if created.IsError || svc.input.SessionMode != routines.SessionReuse || svc.input.Schedule.Kind != routines.ScheduleCron || !svc.input.Enabled || !svc.input.DeleteAfterSuccess || !svc.input.ArchiveSessionAfterSuccess {
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

func TestRoutineToolPatchKeepsUnspecifiedFields(t *testing.T) {
	stored := state.Routine{
		ID: "routine-1", Name: "Review", Prompt: "Old prompt", Directory: "/repo", RemoteID: "local", Agent: "plan", Model: "openai/gpt-5.4",
		SessionMode: routines.SessionReuse, ScheduleKind: routines.ScheduleCron, ScheduleConfigJSON: `{"cron":"0 9 * * 1-5","timezone":"Europe/Brussels"}`,
		PermissionRulesJSON: `[{"permission":"bash","pattern":"git diff *","action":"allow"}]`, Enabled: true, ArchiveSessionAfterSuccess: true,
	}
	svc := &fakeRoutineService{stored: &stored}
	srv := routineServer(t, svc)

	patched := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1", "prompt": "New prompt", "cron": "0 8 * * *"})
	in := svc.input
	if patched.IsError || svc.id != "routine-1" || in.Prompt != "New prompt" || in.Name != "Review" || in.Agent != "plan" || in.Model != "openai/gpt-5.4" ||
		in.SessionMode != routines.SessionReuse || in.Schedule.Kind != routines.ScheduleCron || in.Schedule.Cron != "0 8 * * *" || in.Schedule.Timezone != "Europe/Brussels" ||
		!in.Enabled || !in.ArchiveSessionAfterSuccess || len(in.PermissionRules) != 1 || in.PermissionRules[0].Pattern != "git diff *" || in.KeepSchedule {
		t.Fatalf("patch result = %q, input = %#v", resultText(patched), in)
	}

	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1", "enabled": false, "agent": "", "permission_rules": ""}); r.IsError || svc.input.Enabled || svc.input.Agent != "" || len(svc.input.PermissionRules) != 0 || svc.input.Prompt != "Old prompt" || !svc.input.KeepSchedule {
		t.Fatalf("clearing patch = %q, input = %#v", resultText(r), svc.input)
	}
	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1", "schedule_kind": "once", "at": float64(4_000_000_000_000)}); r.IsError || svc.input.Schedule.Kind != routines.ScheduleOnce || svc.input.Schedule.At.UnixMilli() != 4_000_000_000_000 {
		t.Fatalf("schedule patch = %q, input = %#v", resultText(r), svc.input.Schedule)
	}

	stored.ScheduleKind, stored.ScheduleConfigJSON = routines.ScheduleTimeout, `{"dueAt":1}`
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "patch", "routine_id": "routine-1", "schedule_kind": "timeout"}, "timeout has already elapsed"},
		{map[string]any{"action": "patch", "routine_id": "routine-1", "timeout_ms": float64(-1)}, "invalid routine"},
		{map[string]any{"action": "patch", "routine_id": "routine-1", "timeout_ms": float64(60_000), "permission_rules": "{"}, "permission_rules: invalid JSON"},
	} {
		if r := callTool(t, srv, "routines", test.args); !r.IsError || !strings.Contains(resultText(r), test.want) {
			t.Fatalf("args %#v: %q", test.args, resultText(r))
		}
	}
	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1", "name": "x"}); r.IsError || !svc.input.KeepSchedule {
		t.Fatalf("name-only patch on an elapsed timeout = %q", resultText(r))
	}
	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1", "timeout_ms": float64(60_000)}); r.IsError || svc.input.Schedule.Timeout.Milliseconds() != 60_000 {
		t.Fatalf("timeout patch = %q", resultText(r))
	}
	stored.ScheduleConfigJSON = "{"
	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1"}); !r.IsError || !strings.Contains(resultText(r), "stored schedule is unreadable") {
		t.Fatalf("unreadable schedule = %q", resultText(r))
	}
	stored.ScheduleConfigJSON, stored.PermissionRulesJSON = "{}", "["
	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "routine-1", "timeout_ms": float64(1)}); !r.IsError || !strings.Contains(resultText(r), "stored permission rules are unreadable") {
		t.Fatalf("unreadable rules = %q", resultText(r))
	}
	svc.stored, svc.err = nil, state.ErrRoutineNotFound
	if r := callTool(t, srv, "routines", map[string]any{"action": "patch", "routine_id": "missing"}); !r.IsError || resultText(r) != "routine not found" {
		t.Fatalf("missing = %q", resultText(r))
	}
}
