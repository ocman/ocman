package projectforge

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/module"
)

type recordingRouter struct {
	kinds    map[string]string
	handlers map[string]http.HandlerFunc
}

func (r *recordingRouter) add(kind, pattern string, h http.HandlerFunc) {
	r.kinds[pattern], r.handlers[pattern] = kind, h
}
func (r *recordingRouter) Handle(p string, h http.HandlerFunc)       { r.add("handle", p, h) }
func (r *recordingRouter) Local(p string, h http.HandlerFunc)        { r.add("local", p, h) }
func (r *recordingRouter) Setting(p string, h http.HandlerFunc)      { r.add("setting", p, h) }
func (r *recordingRouter) LocalSetting(p string, h http.HandlerFunc) { r.add("localsetting", p, h) }

// The forge proxies and the launch are localhost-only, upstreams and the
// templates need only a login, and a wrong method is 405 before any guard.
func TestRoutesGuards(t *testing.T) {
	var guarded []string
	guard := func(name string) func(http.HandlerFunc) http.HandlerFunc {
		return func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				guarded = append(guarded, name)
				w.WriteHeader(http.StatusTeapot)
			}
		}
	}
	m := NewModule(Deps{Guards: module.Guards{Auth: guard("auth"), Localhost: guard("local")}})
	r := &recordingRouter{kinds: map[string]string{}, handlers: map[string]http.HandlerFunc{}}
	m.Routes(r)
	mux := http.NewServeMux()
	for pattern, handler := range r.handlers {
		mux.HandleFunc(pattern, handler)
	}
	for pattern, want := range map[string]struct{ method, guard, kind string }{
		"/api/project/upstreams":         {http.MethodGet, "auth", "handle"},
		"/api/project/prs":               {http.MethodGet, "local", "handle"},
		"/api/project/issues":            {http.MethodGet, "local", "handle"},
		"/api/project/pr-checks":         {http.MethodGet, "local", "handle"},
		"/api/project/pr-mergeability":   {http.MethodGet, "local", "handle"},
		"/api/project/forge-user":        {http.MethodGet, "local", "handle"},
		"/api/project/handle":            {http.MethodPost, "local", "handle"},
		"/api/settings/prompt-templates": {http.MethodGet, "auth", "setting"},
	} {
		registered := pattern
		if want.kind == "handle" {
			registered = want.method + " " + pattern
		}
		if r.handlers[registered] == nil || r.kinds[registered] != want.kind {
			t.Fatalf("%s: registered as %q", registered, r.kinds[registered])
		}
		guarded = nil
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(want.method, pattern, nil))
		if rr.Code != http.StatusTeapot || len(guarded) != 1 || guarded[0] != want.guard {
			t.Fatalf("%s: status = %d, guards = %v, want %s", pattern, rr.Code, guarded, want.guard)
		}
		if want.kind == "setting" {
			continue // GET and POST share the mount; the handler checks the method.
		}
		guarded = nil
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, pattern, nil))
		if rr.Code != http.StatusMethodNotAllowed || len(guarded) != 0 {
			t.Fatalf("%s PUT: status = %d, guards = %v", pattern, rr.Code, guarded)
		}
	}
	if len(r.handlers) != 8 {
		t.Fatalf("routes = %v", r.kinds)
	}
}

func TestClassifyRemotes(t *testing.T) {
	m := newTestModule()
	m.KnowsForgejo = func(host string) bool { return host == "code.example.com" }
	got := m.classify([]forge.Remote{
		{Name: "origin", Host: "github.com", Repo: "alice/app", URL: "https://token@github.com/alice/app"},
		{Name: "fj", Host: "code.example.com", Repo: "infra/app"},
		{Name: "unknown", Host: "git.example.org", Repo: "a/b"},
		{Name: "bad", Host: "github.com", Repo: "../etc"},
		{Name: "", Host: "github.com", Repo: "a/b"},
		{Name: "evil", Host: "user@github.com", Repo: "a/b"},
	})
	if len(got) != 2 || got[0].Type != forge.RemoteTypeGitHub || got[1].Type != forge.RemoteTypeForgejo {
		t.Fatalf("classified = %+v", got)
	}
	if got[0].URL != "" {
		t.Fatal("remote URL (possibly with credentials) reached the hub's response")
	}
	m.KnowsForgejo = nil
	if got := m.classify([]forge.Remote{{Name: "fj", Host: "code.example.com", Repo: "infra/app"}}); len(got) != 0 {
		t.Fatalf("unconfigured Forgejo host classified: %+v", got)
	}
}

func TestWriteRateLimitRetryAfter(t *testing.T) {
	rr := httptest.NewRecorder()
	if writeRateLimit(rr, forge.RateLimit{}) || rr.Code != http.StatusOK {
		t.Fatal("unlimited response was written")
	}
	rr = httptest.NewRecorder()
	if !writeRateLimit(rr, forge.RateLimit{Limited: true, ResetAt: time.Now().Add(90 * time.Second)}) {
		t.Fatal("limited response not written")
	}
	retry, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if rr.Code != http.StatusTooManyRequests || err != nil || retry < 89 || retry > 90 {
		t.Fatalf("status = %d, Retry-After = %q", rr.Code, rr.Header().Get("Retry-After"))
	}
	rr = httptest.NewRecorder()
	writeRateLimit(rr, forge.RateLimit{Limited: true, ResetAt: time.Now().Add(-time.Minute)})
	if rr.Header().Get("Retry-After") != "1" {
		t.Fatalf("past reset Retry-After = %q, want 1", rr.Header().Get("Retry-After"))
	}
}

func TestHandleRequestValidate(t *testing.T) {
	ok := handleRequest{Dir: "/repo", RemoteID: "local", Remote: "origin", Type: "pr", Number: 1, Mode: "worktree", Action: "review"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*handleRequest){
		"dir":            func(r *handleRequest) { r.Dir = "" },
		"relative":       func(r *handleRequest) { r.Dir = "repo" },
		"owner":          func(r *handleRequest) { r.RemoteID = " " },
		"remote":         func(r *handleRequest) { r.Remote = "" },
		"type":           func(r *handleRequest) { r.Type = "mr" },
		"number":         func(r *handleRequest) { r.Number = 0 },
		"mode":           func(r *handleRequest) { r.Mode = "tmux" },
		"issue review":   func(r *handleRequest) { r.Type = "issue" },
		"unknown action": func(r *handleRequest) { r.Action = "merge" },
	} {
		req := ok
		mutate(&req)
		if req.validate() == nil {
			t.Errorf("%s: invalid request accepted", name)
		}
	}
}
