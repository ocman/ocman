package routines

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/state"
)

func TestWhitespaceProjectPatchKeepsScheduledRoutineValid(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	input.Schedule = Schedule{Kind: ScheduleCron, Cron: "0 9 * * *", Timezone: "UTC"}
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"action": "patch", "routine_id": routine.ID, "directory": " \t "}
	result, err := (&routineTools{svc: h.svc}).handle(t.Context(), req)
	if err != nil || !result.IsError {
		t.Fatalf("patch should fail: %+v, %v", result, err)
	}
	stored, err := h.svc.Get(t.Context(), routine.ID)
	if err != nil || stored.Directory != routine.Directory || stored.NextDueAt != routine.NextDueAt {
		t.Fatalf("changed routine: %+v, %v", stored, err)
	}
	h.now.Store(routine.NextDueAt)
	if err := h.svc.Tick(t.Context()); err != nil {
		t.Fatalf("scheduled dispatch: %v", err)
	}
	runs, err := h.svc.History(t.Context(), routine.ID)
	if err != nil || len(runs) != 1 || runs[0].Directory != "/repo" || runs[0].Trigger != "schedule" {
		t.Fatalf("scheduled run: %+v, %v", runs, err)
	}
}

func TestCanonicalProjectKeyFailsClosed(t *testing.T) {
	h, _ := projectHarness(t)
	for _, tc := range []struct {
		name     string
		projects []db.ProjectStats
		err      error
	}{
		{name: "unknown"},
		{name: "archived", projects: []db.ProjectStats{{ProjectKey: "git:forge/repo", Directory: "/repo", Archived: true}}},
		{name: "other owner", projects: []db.ProjectStats{{ProjectKey: "git:forge/repo", Directory: "/repo", RemoteID: "box"}}},
		{name: "ambiguous", projects: []db.ProjectStats{{ProjectKey: "git:forge/repo", Directory: "/repo"}, {ProjectKey: "git:forge/repo", Directory: "/other"}}},
		{name: "catalog error", err: errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.svc.projectCatalog = func(context.Context) ([]db.ProjectStats, error) { return tc.projects, tc.err }
			if _, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "local"}, "git:forge/repo"); err == nil {
				t.Fatal("accepted invalid project")
			}
		})
	}
	h.svc.projectCatalog = nil
	if _, err := h.svc.launchProject(t.Context(), state.Routine{}, "dir:local:/repo"); err == nil {
		t.Fatal("accepted absent catalog")
	}
}

func TestCanonicalDirectoryKeyLaunch(t *testing.T) {
	h, host := projectHarness(t)
	host.projects[0].ProjectKey = ""
	h.svc.projectCatalog = func(context.Context) ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/repo", RemoteID: "local", ProjectKey: "dir:local:/repo"}}, nil
	}
	got, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "local"}, "dir:local:/repo")
	if err != nil || strings.TrimSpace(got) != "/repo" {
		t.Fatalf("canonical key: %q, %v", got, err)
	}
	h.svc.projectCatalog = func(context.Context) ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/repo", ProjectKey: "git:forge.test/org/repo"}}, nil
	}
	if got, err := h.svc.launchProject(t.Context(), state.Routine{}, "git:forge.test/org/repo"); err != nil || got != "/repo" {
		t.Fatalf("canonical git key: %q, %v", got, err)
	}
}
