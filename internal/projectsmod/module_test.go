package projectsmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// host serves this machine's projects and records opencode stops.
type host struct {
	hostsvc.Host
	projects []db.ProjectStats
	err      error
	stopped  []string
}

func (*host) RemoteID() string { return state.LocalRemoteID }
func (h *host) Projects(context.Context) ([]db.ProjectStats, error) {
	return append([]db.ProjectStats(nil), h.projects...), h.err
}
func (h *host) StopProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	h.stopped = append(h.stopped, req.ProjectDir)
	return errors.New("no tmux session") // best effort
}

type fixture struct {
	m         *Module
	store     *state.DB
	host      *host
	events    []string
	forgotten []string
}

func newFixture(t *testing.T, projects ...db.ProjectStats) *fixture {
	f := &fixture{store: statetest.Open(t), host: &host{projects: projects}}
	f.m = NewModule(Deps{
		Store: f.store,
		Local: func() hostsvc.Host { return f.host },
		ResolveOwner: func(http.ResponseWriter, string, string) (hostsvc.Host, bool) {
			return f.host, true
		},
		Broadcast:           func(event string, _ []byte) { f.events = append(f.events, event) },
		DefaultAgent:        func(context.Context) (string, error) { return "build", nil },
		ForgetProjectModels: func(dir string) { f.forgotten = append(f.forgotten, dir) },
	})
	return f
}

func (f *fixture) call(method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	moduletest.Mount(f.m).ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func (f *fixture) list(t *testing.T) []db.ProjectStats {
	t.Helper()
	rec := f.call(http.MethodGet, "/api/projects", "")
	var out []db.ProjectStats
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d %s: %v", rec.Code, rec.Body, err)
	}
	return out
}

func TestListFoldsWorktreesAndAppliesArchive(t *testing.T) {
	f := newFixture(t,
		db.ProjectStats{Directory: "/src/repo", SessionCount: 1, LastUsed: 100},
		db.ProjectStats{Directory: "/src/.worktrees/repo/topic", SessionCount: 2, LastUsed: 200},
	)
	if got := f.list(t); len(got) != 1 || got[0].Directory != "/src/repo" || got[0].SessionCount != 3 || got[0].Archived {
		t.Fatalf("projects = %+v", got)
	}
	if rec := f.call(http.MethodPost, "/api/project/archive", `{"directory":"/src/.worktrees/repo/topic","archived":true}`); rec.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", rec.Code, rec.Body)
	}
	if len(f.host.stopped) != 1 || f.host.stopped[0] != "/src/repo" {
		t.Fatalf("stopped = %v", f.host.stopped)
	}
	if got := f.list(t); !got[0].Archived {
		t.Fatalf("archived project listed as %+v", got)
	}
	if rec := f.call(http.MethodPost, "/api/project/archive", `{"directory":"/src/repo","archived":false}`); rec.Code != http.StatusOK {
		t.Fatalf("unarchive = %d %s", rec.Code, rec.Body)
	}
	if got := f.list(t); got[0].Archived {
		t.Fatalf("unarchived project listed as %+v", got)
	}
	if rec := f.call(http.MethodPost, "/api/project/archive", `{"directory":" "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank directory = %d", rec.Code)
	}
	f.host.err = errors.New("history unavailable")
	if rec := f.call(http.MethodGet, "/api/projects", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list failure = %d", rec.Code)
	}
}

func TestMergeAndResolve(t *testing.T) {
	f := newFixture(t,
		db.ProjectStats{Directory: "/source", LastUsed: 2},
		db.ProjectStats{Directory: "/target", LastUsed: 1},
	)
	for body, want := range map[string]int{
		`{"source":{"directory":"/source"},"target":{"directory":"/missing"}}`: http.StatusBadRequest,
		`{"source":{"directory":"/source"},"target":{"directory":"/source"}}`:  http.StatusBadRequest,
		`{`: http.StatusBadRequest,
	} {
		if rec := f.call(http.MethodPost, "/api/project/merge", body); rec.Code != want {
			t.Errorf("%s = %d, want %d", body, rec.Code, want)
		}
	}
	if rec := f.call(http.MethodPost, "/api/project/merge", `{"source":{"directory":"/source"},"target":{"directory":"/target"}}`); rec.Code != http.StatusOK {
		t.Fatalf("merge = %d %s", rec.Code, rec.Body)
	}
	if len(f.events) != 1 || f.events[0] != "ocman.projects.changed" {
		t.Fatalf("events = %v", f.events)
	}
	if got := f.list(t); len(got) != 1 || got[0].Directory != "/target" {
		t.Fatalf("merged projects = %+v", got)
	}
	rec := f.call(http.MethodPost, "/api/projects/resolve", `{"checkouts":[{"directory":"/source"}]}`)
	var ids []struct{ Directory string }
	if err := json.Unmarshal(rec.Body.Bytes(), &ids); err != nil || len(ids) != 1 || ids[0].Directory != "/target" {
		t.Fatalf("resolve = %d %s: %v", rec.Code, rec.Body, err)
	}
	if rec := f.call(http.MethodPost, "/api/projects/resolve", `{"checkouts":[{"directory":""}]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank checkout = %d", rec.Code)
	}
}

func TestSettingsRoute(t *testing.T) {
	f := newFixture(t)
	if rec := f.call(http.MethodPost, "/api/project/settings", `{"directory":"/repo","models":["anthropic/claude"]}`); rec.Code != http.StatusOK {
		t.Fatalf("POST models = %d %s", rec.Code, rec.Body)
	}
	if len(f.forgotten) != 1 || f.forgotten[0] != "/repo" {
		t.Fatalf("forgotten = %v", f.forgotten)
	}
	if rec := f.call(http.MethodPost, "/api/project/settings", `{"directory":"/repo","defaults":{"agent":"plan","worktree":"current"}}`); rec.Code != http.StatusOK {
		t.Fatalf("POST defaults = %d %s", rec.Code, rec.Body)
	}
	rec := f.call(http.MethodGet, "/api/project/settings?dir=/repo", "")
	var got struct {
		Models       []string
		DefaultAgent string
		Defaults     *Defaults
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Models) != 1 || got.DefaultAgent != "plan" || got.Defaults == nil || got.Defaults.Worktree != "current" {
		t.Fatalf("GET = %d %s: %v", rec.Code, rec.Body, err)
	}
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/project/settings", ""},
		{http.MethodPost, "/api/project/settings", `{"models":[]}`},
		{http.MethodPost, "/api/project/settings", `{"directory":"/repo","defaults":{"worktree":"elsewhere"}}`},
		{http.MethodPost, "/api/project/settings", `{"directory":"/repo","models":["` + strings.Repeat("m", 1000) + `"]}`},
	} {
		if rec := f.call(tc.method, tc.target, tc.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s = %d", tc.method, tc.target, tc.body, rec.Code)
		}
	}
	if rec := f.call(http.MethodDelete, "/api/project/settings", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE = %d", rec.Code)
	}
}

func TestDefaultsValidation(t *testing.T) {
	for _, d := range []Defaults{{Agent: " plan"}, {Worktree: "x"}, {PermissionMode: "unknown"}, {Model: strings.Repeat("m", 1000)}} {
		if err := d.validate(); err == nil {
			t.Errorf("%+v validated", d)
		}
	}
	if DefaultsKey("/src/repo", "") != DefaultsKey("/src/.worktrees/repo/topic", "local") {
		t.Fatal("defaults are not keyed by local owner and folded root")
	}
}
