package routines

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineWorktreeHost struct {
	*testHost
	request   hostsvc.WorktreeSessionRequest
	removed   []hostsvc.RemoveWorktreeRequest
	removeErr error
	createErr error
	path      string
	worktrees []git.Worktree
	listErr   error
}

type routineWorktreePlatform struct {
	*testPlatform
	sessions []db.Session
	listErr  error
}

func (p routineWorktreePlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return p.sessions, p.listErr
}

func (h *routineWorktreeHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.request = req
	return &hostsvc.WorktreeSessionResult{SessionID: "worktree-session", WorktreePath: h.path, Branch: req.Branch}, h.createErr
}

func (h *routineWorktreeHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	if h.listErr != nil {
		return nil, h.listErr
	}
	if h.worktrees != nil {
		return h.worktrees, nil
	}
	return []git.Worktree{{Path: h.path, Branch: h.request.Branch}}, nil
}

func TestRoutineWorktreeCleanupGuards(t *testing.T) {
	for _, tc := range []struct {
		name            string
		tree            []db.Session
		worktrees       []git.Worktree
		unavailable     bool
		listErr         error
		sessions        []db.Session
		sessionErr      error
		missingPlatform bool
		wantError       bool
	}{
		{name: "active child", tree: []db.Session{{ID: "child", Status: db.StatusBusy}}, wantError: true},
		{name: "waiting child", tree: []db.Session{{ID: "child", Status: db.StatusWaiting}}, wantError: true},
		{name: "changed branch", worktrees: []git.Worktree{{Path: "/wt", Branch: "user-work"}}, wantError: true},
		{name: "already removed", worktrees: []git.Worktree{}},
		{name: "unrelated checkout", worktrees: []git.Worktree{{Path: "/another", Branch: "routine/run"}}},
		{name: "disconnected owner", unavailable: true, wantError: true},
		{name: "git failure", listErr: errors.New("git failed"), wantError: true},
		{name: "another busy session", sessions: []db.Session{{ID: "other", Status: db.StatusBusy}}, wantError: true},
		{name: "another waiting session", sessions: []db.Session{{ID: "other", Status: db.StatusWaiting}}, wantError: true},
		{name: "session lookup failed", sessionErr: errors.New("unavailable"), wantError: true},
		{name: "platform disappeared", missingPlatform: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.platforms.Register(routineWorktreePlatform{testPlatform: h.platform, sessions: tc.sessions, listErr: tc.sessionErr})
			owner := &routineWorktreeHost{testHost: h.host, path: "/wt", worktrees: tc.worktrees, listErr: tc.listErr}
			h.svc.router = hostsvc.NewRouter(owner)
			run := state.RoutineRun{ID: "run", Platform: "opencode", Directory: "/repo", RemoteID: "local", Worktree: true, CleanupWorktree: true, WorktreePath: "/wt"}
			if tc.unavailable {
				run.RemoteID = "offline"
			}
			if tc.missingPlatform {
				run.Platform = "unavailable"
			}
			message := h.svc.cleanupRunWorktree(t.Context(), run, &platforms.SessionDetail{SessionTree: tc.tree})
			if (message != "") != tc.wantError || len(owner.removed) != 0 {
				t.Fatalf("message = %q, removed = %#v", message, owner.removed)
			}
		})
	}
}

func TestRoutineWorktreeLaunchFailureAndQuietPeriod(t *testing.T) {
	h := newHarness(t)
	h.svc.platforms.Register(routineWorktreePlatform{testPlatform: h.platform})
	owner := &routineWorktreeHost{testHost: h.host, path: "/wt", createErr: errors.New("checkout failed")}
	h.svc.router = hostsvc.NewRouter(owner)
	input := validInput()
	input.Worktree, input.CleanupWorktree = true, true
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.RunNow(t.Context(), routine.ID)
	if err != nil || run.State != RunFailure || len(owner.removed) != 0 {
		t.Fatalf("run = %#v, %v", run, err)
	}
	owner.createErr = nil
	h.now.Add(1)
	run, err = h.svc.RunNow(t.Context(), routine.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.platform.timeUpdated = h.now.Load()
	h.platform.setStatus(db.StatusDone)
	if err := h.svc.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(owner.removed) != 0 {
		t.Fatal("removed before quiet period")
	}
	h.now.Add(settleQuietPeriod.Milliseconds())
	if err := h.svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(owner.removed) != 1 {
		t.Fatalf("cleanup not recovered: %#v", owner.removed)
	}
	finished, err := h.db.GetRoutineRun(t.Context(), run.ID)
	if err != nil || finished.State != RunSuccess {
		t.Fatalf("finished = %#v, %v", finished, err)
	}
}

func (h *routineWorktreeHost) RemoveWorktree(_ context.Context, req hostsvc.RemoveWorktreeRequest) error {
	h.removed = append(h.removed, req)
	return h.removeErr
}

func TestRoutineWorktreeLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cleanup    bool
		status     db.SessionStatus
		removeErr  error
		wantRemove bool
	}{
		{"opted in", true, db.StatusDone, nil, true},
		{"retained", false, db.StatusDone, nil, false},
		{"failed", true, db.StatusError, nil, false},
		{"busy", true, db.StatusBusy, nil, false},
		{"dirty", true, db.StatusDone, git.ErrWorktreeDirty, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			owner := &routineWorktreeHost{testHost: h.host, path: "/worktrees/run", removeErr: tc.removeErr}
			h.svc.platforms.Register(routineWorktreePlatform{testPlatform: h.platform})
			h.svc.router = hostsvc.NewRouter(owner)
			input := validInput()
			input.Worktree, input.CleanupWorktree = true, tc.cleanup
			routine, err := h.svc.Create(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.RunNow(t.Context(), routine.ID)
			if err != nil {
				t.Fatal(err)
			}
			if run.WorktreePath != owner.path || run.SessionID != "worktree-session" || !owner.request.MustCreateBranch || owner.request.Branch != "routine/"+run.ID {
				t.Fatalf("run = %#v, request = %#v", run, owner.request)
			}
			// The run snapshot owns cleanup even after the definition changes.
			input.CleanupWorktree = false
			if _, err := h.svc.Update(t.Context(), routine.ID, input); err != nil {
				t.Fatal(err)
			}
			h.platform.setStatus(tc.status)
			if err := h.svc.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			if (len(owner.removed) != 0) != tc.wantRemove {
				t.Fatalf("removed = %#v", owner.removed)
			}
			if tc.wantRemove && (owner.removed[0].Force || owner.removed[0].Path != owner.path || owner.removed[0].Dir != "/repo") {
				t.Fatalf("unsafe removal: %#v", owner.removed)
			}
			finished, err := h.db.GetRoutineRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.removeErr != nil && (finished.State != RunSuccess || finished.Error == "") {
				t.Fatalf("cleanup error lost: %#v", finished)
			}
		})
	}
}

func TestRoutineWorkspaceValidationAndDefaults(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil || routine.Worktree || routine.CleanupWorktree {
		t.Fatalf("default = %#v, %v", routine, err)
	}
	for _, mode := range []string{SessionReuse, SessionExisting} {
		input.SessionMode, input.SessionID, input.Worktree = mode, "session", true
		if _, err := h.svc.Create(t.Context(), input); !errors.Is(err, ErrValidation) {
			t.Fatalf("mode %s accepted: %v", mode, err)
		}
	}
	input = validInput()
	input.CleanupWorktree = true
	if _, err := h.svc.Create(t.Context(), input); !errors.Is(err, ErrValidation) {
		t.Fatalf("checkout cleanup accepted: %v", err)
	}
	input.Worktree = true
	routine, err = h.svc.Update(t.Context(), routine.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := InputFromRoutine(routine, h.svc.now())
	if err != nil || !restored.Worktree || !restored.CleanupWorktree {
		t.Fatalf("restored = %#v, %v", restored, err)
	}
	stored, err := h.db.GetRoutine(t.Context(), routine.ID)
	if err != nil || !stored.Worktree || !stored.CleanupWorktree {
		t.Fatalf("stored = %#v, %v", stored, err)
	}
}
