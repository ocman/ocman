package routines

import (
	"context"
	"errors"
	"fmt"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestModuleRunStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	NewModule(Deps{Store: newHarness(t).db}).Run(ctx)
}

func TestModuleTickRecoversPanic(t *testing.T) {
	(&Module{svc: &Service{}}).tick(t.Context()) // nil store panics inside Tick
}

func TestSummarizeRoutineRuns(t *testing.T) {
	runs := []state.RoutineRun{
		{State: "success", StartedAt: 100, FinishedAt: 300, Platform: "remote", SessionID: "s"},
		{State: "failure", StartedAt: 400, FinishedAt: 800, Platform: "remote", SessionID: "s"},
		{State: "running", StartedAt: 900, Platform: "remote", SessionID: "child"},
		{State: "interrupted", StartedAt: 1000, FinishedAt: 900, Platform: "offline", SessionID: "s"},
		{State: "failure"},
	}
	var calls int
	stats := summarizeRuns(t.Context(), runs, func(_ context.Context, key state.Key) (map[string]platforms.Usage, error) {
		calls++
		if key.Platform == "offline" {
			return nil, platforms.ErrNotFound
		}
		if key.SessionID == "child" {
			return map[string]platforms.Usage{"child": {Cost: 2, EstCost: 3}}, nil
		}
		return map[string]platforms.Usage{"s": {Cost: 1, EstCost: 2}, "child": {Cost: 2, EstCost: 3}}, nil
	})
	if stats.TotalRuns != 5 || stats.States["failure"] != 2 || stats.States["running"] != 1 || stats.AverageDuration == nil || *stats.AverageDuration != 300 {
		t.Fatalf("run stats: %+v", stats)
	}
	if calls != 3 || stats.CostSessions != 2 || stats.MissingSessions != 1 || stats.TotalCost != 3 || stats.TotalEstCost != 5 {
		t.Fatalf("cost stats: %+v, calls=%d", stats, calls)
	}
	empty := summarizeRuns(t.Context(), nil, nil)
	if empty.TotalRuns != 0 || empty.AverageDuration != nil || empty.CostSessions != 0 {
		t.Fatalf("empty: %+v", empty)
	}
}

func TestModuleIdentity(t *testing.T) {
	m := NewModule(Deps{Store: newHarness(t).db})
	if m.Name() != "routines" || m.Service() == nil || len(m.Tools()) == 0 {
		t.Fatalf("module identity: %q %v %d", m.Name(), m.Service(), len(m.Tools()))
	}
}

func TestRoutineToolErrorMessages(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("bad: %w", ErrValidation): "bad: " + ErrValidation.Error(),
		state.ErrRoutineNotFound:             "routine not found",
		ErrNameConflict:                      "routine name already exists",
		state.ErrRoutineRunActive:            "routine already has an active shared-session run",
		errors.New("disk on fire"):           "routine request failed",
	} {
		res := routineToolError(err)
		if !res.IsError || len(res.Content) != 1 || res.Content[0].(mcplib.TextContent).Text != want {
			t.Errorf("%v → %+v, want %q", err, res, want)
		}
	}
}
