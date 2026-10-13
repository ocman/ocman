package idleprojects

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type fakePlatform struct {
	platforms.Platform
	sessions []db.Session
	fresh    func(db.Session) (*platforms.SessionDetail, error)
	err      error
}

func (p *fakePlatform) ID() platforms.ID { return "test" }
func (p *fakePlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return p.sessions, p.err
}
func (p *fakePlatform) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	for _, s := range p.sessions {
		if s.ID == id {
			if p.fresh != nil {
				return p.fresh(s)
			}
			return &platforms.SessionDetail{Session: &s}, nil
		}
	}
	return nil, platforms.ErrNotFound
}

type fakeRuntime struct {
	roots       []string
	stops       int
	err         error
	before      func()
	directories []string
}

func (r *fakeRuntime) ManagedIdleProjects(context.Context) ([]string, error) { return r.roots, r.err }
func (r *fakeRuntime) CullIdleProject(ctx context.Context, _ string, dirs []string, check func(context.Context) (bool, error)) (bool, error) {
	r.directories = dirs
	if r.before != nil {
		r.before()
	}
	idle, err := check(ctx)
	if err != nil || !idle {
		return false, err
	}
	r.stops++
	r.roots = nil
	return true, nil
}

func setup(t *testing.T) (*Module, *fakePlatform, *fakeRuntime) {
	t.Helper()
	s, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p := &fakePlatform{sessions: []db.Session{{ID: "s", Directory: "/repo", TimeUpdated: 100, Status: db.StatusDone}}}
	r := &fakeRuntime{roots: []string{"/repo"}}
	m := New(Deps{Store: s, Targets: func() []Target { return []Target{{Platform: p, Runtime: r}} }})
	if err := s.SetSetting(t.Context(), setting, "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveSession(t.Context(), "test", "s", 100); err != nil {
		t.Fatal(err)
	}
	return m, p, r
}

func sweep(t *testing.T, m *Module, at time.Time) {
	t.Helper()
	if err := m.sweep(t.Context(), at); err != nil {
		t.Fatal(err)
	}
}

func TestCullAfterTimeoutAndResetOnActivity(t *testing.T) {
	m, p, r := setup(t)
	now := time.Now()
	sweep(t, m, now)
	sweep(t, m, now.Add(59*time.Second))
	if r.stops != 0 {
		t.Fatal("stopped before timeout")
	}
	p.sessions[0].Status = db.StatusBusy
	sweep(t, m, now.Add(time.Minute))
	p.sessions[0].Status = db.StatusDone
	sweep(t, m, now.Add(2*time.Minute))
	sweep(t, m, now.Add(3*time.Minute))
	if r.stops != 1 || len(m.idle) != 0 {
		t.Fatalf("stops=%d timers=%v", r.stops, m.idle)
	}
}

func TestEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *Module, *fakePlatform)
		want   bool
	}{
		{"archived", func(*testing.T, *Module, *fakePlatform) {}, true},
		{"unarchived", func(t *testing.T, m *Module, _ *fakePlatform) {
			must(t, m.deps.Store.UnarchiveSession(t.Context(), "test", "s"))
		}, false},
		{"new activity", func(_ *testing.T, _ *Module, p *fakePlatform) { p.sessions[0].TimeUpdated++ }, false},
		{"busy", func(_ *testing.T, _ *Module, p *fakePlatform) { p.sessions[0].Status = db.StatusBusy }, false},
		{"waiting", func(_ *testing.T, _ *Module, p *fakePlatform) { p.sessions[0].Status = db.StatusWaiting }, false},
		{"limited", func(_ *testing.T, _ *Module, p *fakePlatform) { p.sessions[0].Status = db.StatusLimited }, false},
		{"empty", func(_ *testing.T, _ *Module, p *fakePlatform) { p.sessions = nil }, false},
		{"worktree", func(_ *testing.T, _ *Module, p *fakePlatform) { p.sessions[0].Directory = "/.worktrees/repo/feature" }, false},
		{"managed worktree", func(_ *testing.T, _ *Module, p *fakePlatform) {
			p.sessions[0].Directory = "/src/.worktrees/repo/feature"
		}, false},
		{"completed subagent", func(_ *testing.T, _ *Module, p *fakePlatform) {
			p.sessions = append(p.sessions, db.Session{ID: "child", ParentID: "s", Directory: "/repo", Status: db.StatusDone})
		}, true},
		{"active subagent", func(_ *testing.T, _ *Module, p *fakePlatform) {
			p.sessions = append(p.sessions, db.Session{ID: "child", ParentID: "s", Directory: "/repo", Status: db.StatusWaiting})
		}, false},
		{"naming helper", func(_ *testing.T, _ *Module, p *fakePlatform) {
			p.sessions = append(p.sessions, db.Session{ID: "helper", Title: "(worktree-name subagent)", Directory: "/repo", Status: db.StatusDone})
		}, true},
		{"archived queue", func(t *testing.T, m *Module, _ *fakePlatform) {
			must(t, m.deps.Store.EnqueueMessage(t.Context(), state.QueuedMessage{Platform: "test", SessionID: "s", Text: "later"}))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, p, _ := setup(t)
			tc.change(t, m, p)
			_, _, idle, err := m.eligible(t.Context(), p, "/repo", false)
			if err != nil || idle != tc.want {
				t.Fatalf("idle=%v err=%v", idle, err)
			}
		})
	}
}

func TestFinalRecheckPreservesNewWork(t *testing.T) {
	for _, change := range []string{"unarchive", "busy", "child", "error", "missing", "activity"} {
		t.Run(change, func(t *testing.T) {
			m, p, r := setup(t)
			now := time.Now()
			sweep(t, m, now)
			p.fresh = func(s db.Session) (*platforms.SessionDetail, error) {
				d := &platforms.SessionDetail{Session: &s}
				switch change {
				case "unarchive":
					must(t, m.deps.Store.UnarchiveSession(t.Context(), "test", "s"))
				case "busy":
					s.Status = db.StatusBusy
				case "child":
					d.SessionTree = []db.Session{{Status: db.StatusWaiting}}
				case "error":
					return nil, errors.New("unavailable")
				case "missing":
					return nil, nil
				case "activity":
					s.TimeUpdated++
				}
				return d, nil
			}
			sweep(t, m, now.Add(time.Minute))
			if r.stops != 0 {
				t.Fatal("culled a project that became active during the final check")
			}
		})
	}
}

func TestDisabledAndFailureResetTimers(t *testing.T) {
	m, p, r := setup(t)
	now := time.Now()
	sweep(t, m, now)
	must(t, m.deps.Store.SetSetting(t.Context(), setting, "0"))
	sweep(t, m, now.Add(time.Minute))
	if len(m.idle) != 0 || r.stops != 0 {
		t.Fatal("disabled culling retained timers")
	}
	must(t, m.deps.Store.SetSetting(t.Context(), setting, "1"))
	sweep(t, m, now.Add(2*time.Minute))
	p.err = errors.New("listing failed")
	sweep(t, m, now.Add(3*time.Minute))
	if len(m.idle) != 0 {
		t.Fatal("uncertain listing retained timer")
	}
	p.err = nil
	r.err = errors.New("runtime failed")
	if err := m.sweep(t.Context(), now); err == nil {
		t.Fatal("lost runtime error")
	}
}

func TestSettings(t *testing.T) {
	m, _, _ := setup(t)
	for _, tc := range []struct {
		method, body string
		code         int
	}{
		{"GET", "", 200}, {"POST", `{"minutes":0}`, 200}, {"POST", `{"minutes":43200}`, 200},
		{"POST", `{"minutes":-1}`, 400}, {"POST", `{"minutes":43201}`, 400}, {"POST", `{"minutes":1.5}`, 400},
		{"POST", "invalid", 400}, {"DELETE", "", 405},
	} {
		rr := httptest.NewRecorder()
		m.settings(rr, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))
		if rr.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.body, rr.Code, rr.Body.String())
		}
	}
	must(t, m.deps.Store.SetSetting(t.Context(), setting, "invalid"))
	rr := httptest.NewRecorder()
	m.settings(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != 500 {
		t.Fatal(rr.Code)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
