package idleprojects

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestSharedRuntimeMembership(t *testing.T) {
	for _, status := range []db.SessionStatus{db.StatusBusy, db.StatusDone} {
		m, p, r := setup(t)
		r.membership = map[string]string{"/arbitrary/linked/subdir": "/repo"}
		p.sessions = append(p.sessions, db.Session{ID: "other", Directory: "/arbitrary/linked/subdir", Status: status})
		_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
		if err != nil || idle {
			t.Fatalf("shared %s session was missed: idle=%v err=%v", status, idle, err)
		}
	}
}

func TestMembershipResolutionFailsClosed(t *testing.T) {
	m, _, r := setup(t)
	r.resolveError = errors.New("git probe failed")
	_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err == nil || idle {
		t.Fatal("uncertain membership allowed culling")
	}
}

type lifecyclePlatform struct {
	*fakePlatform
	lifecycle *platforms.SessionLifecycle
	err       error
}

func (p *lifecyclePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return p.lifecycle, p.err
}

func TestFinalCheckUsesBoundedLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  db.SessionStatus
		created int64
		missing bool
		err     error
		idle    bool
	}{
		{"idle", db.StatusDone, 100, false, nil, true},
		{"busy", db.StatusBusy, 100, false, nil, false},
		{"new activity", db.StatusDone, 200, false, nil, false},
		{"missing", db.StatusDone, 100, true, nil, false},
		{"failed read", db.StatusDone, 100, false, errors.New("unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p, r := setup(t)
			p.fresh = func(db.Session) (*platforms.SessionDetail, error) {
				t.Fatal("loaded transcript for idle check")
				return nil, nil
			}
			bounded := &lifecyclePlatform{fakePlatform: p, lifecycle: &platforms.SessionLifecycle{Status: tc.status, LatestMessageCreated: tc.created}, err: tc.err}
			if tc.missing {
				bounded.lifecycle = nil
			}
			_, _, idle, err := m.eligible(t.Context(), Target{Platform: bounded, Runtime: r}, "/repo", true)
			if idle != tc.idle || (err != nil) != (tc.err != nil) {
				t.Fatalf("idle=%v err=%v", idle, err)
			}
		})
	}
}

func TestSharedRuntimeBackgroundMembership(t *testing.T) {
	m, _, r := setup(t)
	r.membership = map[string]string{"/arbitrary/linked/subdir": "/repo"}
	must(t, m.deps.Store.CreateRoutine(t.Context(), state.Routine{ID: "r", Name: "active", Platform: "test", Directory: "/arbitrary/linked/subdir", RemoteID: "local"}))
	_, _, err := m.deps.Store.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "r", State: "running", OccurrenceAt: 1})
	must(t, err)
	_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err != nil || idle {
		t.Fatal("active run in shared worktree was missed", err)
	}
}
