package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type fakeForge struct {
	forge.Forge
	pr          forge.PR
	checks      forge.CIStatus
	remarks     []forge.Remark
	err         error
	checkErr    error
	feedbackErr error
	userErr     error
	limited     bool
	reads       int
}

func (f *fakeForge) LookupPR(context.Context, string, int) (forge.PR, error) {
	f.reads++
	return f.pr, f.err
}
func (f *fakeForge) Checks(context.Context, string, string) (forge.CIStatus, forge.RateLimit, error) {
	if f.checkErr != nil {
		return forge.CIStatus{}, forge.RateLimit{}, f.checkErr
	}
	return f.checks, forge.RateLimit{Limited: f.limited}, f.err
}
func (f *fakeForge) Feedback(context.Context, string, int) ([]forge.Remark, error) {
	if f.feedbackErr != nil {
		return nil, f.feedbackErr
	}
	return f.remarks, f.err
}
func (f *fakeForge) CurrentUser(context.Context) (forge.CurrentUser, error) {
	if f.userErr != nil {
		return forge.CurrentUser{}, f.userErr
	}
	return forge.CurrentUser{Login: "me"}, f.err
}

type fakeHost struct {
	hostsvc.Host
	err error
}

func (h fakeHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	return &hostsvc.ProjectUpstreams{RepoRoot: "/repo", Remotes: []forge.Remote{{Name: "origin", Host: "github.com", Type: forge.RemoteTypeGitHub, Repo: "team/repo"}}}, h.err
}

type fixture struct {
	m        *Module
	f        *fakeForge
	sent     []string
	ownerErr error
	sendErr  error
	ready    bool
	target   bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := state.Open(statetest.Path(t), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	x := &fixture{f: &fakeForge{pr: forge.PR{Number: 42, Title: "Improve setup", Status: "open", URL: "https://github.com/team/repo/pull/42", HeadSHA: "abc", BaseBranch: "main", Branch: "feature"}, checks: forge.CIStatus{State: forge.CIStatePending}}, ready: true, target: true}
	x.m = New(Deps{DB: db, Target: func(w http.ResponseWriter, _ *http.Request, id string) (Target, bool) {
		if !x.target {
			http.Error(w, "no owner", http.StatusServiceUnavailable)
			return Target{}, false
		}
		return Target{"r-box:opencode", id, "/repo", "box"}, true
	}, Owner: func(context.Context, Target) (hostsvc.Host, error) { return fakeHost{}, x.ownerErr }, ResolveForge: func(forge.Remote) (forge.Forge, bool) { return x.f, true }, Ready: func(context.Context, Target) bool { return x.ready }, Enqueue: func(_ context.Context, platform string, req platforms.SendMessageRequest) error {
		if platform != "r-box:opencode" {
			t.Error("wrong platform")
		}
		if x.sendErr == nil {
			x.sent = append(x.sent, req.Message)
		}
		return x.sendErr
	}})
	return x
}
func (x *fixture) request(t *testing.T, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	method := "POST"
	if body == "" {
		method = "GET"
	}
	r := httptest.NewRequest(method, "/api/session/s1/"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	moduletest.Mount(x.m).ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w
}
func (x *fixture) link(t *testing.T) {
	x.request(t, "pull-requests", `{"action":"link","remote":"origin","number":42}`, 200)
}
func (x *fixture) watch(t *testing.T) {
	x.request(t, "pull-requests", `{"action":"watch","host":"github.com","repo":"team/repo","number":42}`, 200)
}
func (x *fixture) only(t *testing.T) Link {
	t.Helper()
	links, err := x.m.links(t.Context(), "r-box:opencode", "s1")
	if err != nil || len(links) != 1 {
		t.Fatalf("links=%v err=%v", links, err)
	}
	return links[0]
}

func TestLinksAndWatchLifecycle(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.link(t)
	if !x.only(t).Watching {
		t.Fatal("relink reset watch")
	}
	x.watch(t)
	x.m.sweep(t.Context())
	if len(x.sent) != 0 {
		t.Fatal("initial snapshot woke agent")
	}
	x.f.checks = forge.CIStatus{State: forge.CIStateFailure, Checks: []forge.Check{{Name: "tests", State: forge.CIStateFailure}}}
	x.m.sweep(t.Context())
	if len(x.sent) != 1 || !strings.Contains(x.sent[0], "tests") {
		t.Fatalf("sent=%v", x.sent)
	}
	x.m.sweep(t.Context())
	if len(x.sent) != 1 {
		t.Fatal("duplicate wake")
	}
	x.f.pr.HeadSHA = "def"
	x.m.sweep(t.Context())
	if len(x.sent) != 2 {
		t.Fatal("new head not evaluated")
	}
	x.request(t, "pull-requests", `{"action":"unwatch","host":"github.com","repo":"team/repo","number":42}`, 200)
	x.f.remarks = []forge.Remark{{ID: "r", Author: "other", Body: "Please fix"}}
	x.m.sweep(t.Context())
	if len(x.sent) != 2 {
		t.Fatal("stopped watch sent")
	}
	w := x.request(t, "pull-requests", "", 200)
	if strings.Contains(w.Body.String(), "Snapshot") || strings.Contains(w.Body.String(), "Pending") {
		t.Fatal("internal watch state leaked")
	}
	x.request(t, "pull-requests", `{"action":"unlink","host":"github.com","repo":"team/repo","number":42}`, 204)
	links, err := x.m.links(t.Context(), "r-box:opencode", "s1")
	if err != nil || len(links) != 0 {
		t.Fatalf("links=%v err=%v", links, err)
	}
}

func TestWatchRecoveryAndOwnerIsolation(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.ownerErr = errors.New("offline")
	x.f.checks.State = forge.CIStateSuccess
	x.m.sweep(t.Context())
	if len(x.sent) != 0 {
		t.Fatal("offline owner received wake")
	}
	x.ownerErr = nil
	x.ready = false
	x.m.sweep(t.Context())
	if len(x.sent) != 0 {
		t.Fatal("busy session received wake")
	}
	x.ready = true
	x.sendErr = errors.New("queue offline")
	x.m.sweep(t.Context())
	if x.only(t).Pending == "" {
		t.Fatal("lost pending update")
	}
	// A new module instance reuses the persisted snapshot and outbox.
	x.m = New(x.m.d)
	x.sendErr = nil
	x.m.sweep(t.Context())
	if len(x.sent) != 1 || x.only(t).Pending != "" {
		t.Fatal("pending wake not recovered")
	}
	x.f.pr.Status = "merged"
	x.m.sweep(t.Context())
	if x.only(t).Watching || len(x.sent) != 2 {
		t.Fatal("merge did not stop and notify")
	}
	x.watchClosed(t)
}
func (x *fixture) watchClosed(t *testing.T) {
	x.request(t, "pull-requests", `{"action":"watch","host":"github.com","repo":"team/repo","number":42}`, 409)
}

func TestWatchBackoffAndCommentLoopLimit(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.f.limited = true
	x.m.sweep(t.Context())
	l := x.only(t)
	if l.Failures != 1 || l.NextAt <= time.Now().UnixMilli() || l.Snapshot == nil {
		t.Fatal("failure did not retain snapshot and back off")
	}
	x.m.sweep(t.Context())
	if x.only(t).Failures != 1 {
		t.Fatal("ignored backoff")
	}
	for i := 1; i < 8; i++ {
		l = x.only(t)
		l.NextAt = 0
		if err := x.m.put(t.Context(), l); err != nil {
			t.Fatal(err)
		}
		x.m.sweep(t.Context())
	}
	if x.only(t).Watching {
		t.Fatal("watch did not stop after repeated failures")
	}
	x.f.limited = false
	x.watch(t)
	for i := 0; i < 10; i++ {
		x.f.remarks = []forge.Remark{{ID: "review", Author: "other", Body: strings.Repeat("x", i+1)}}
		x.m.sweep(t.Context())
	}
	if x.only(t).Watching || len(x.sent) != 10 {
		t.Fatalf("watching=%v sent=%d", x.only(t).Watching, len(x.sent))
	}
}

func TestFeedbackDraftsAndValidation(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.f.remarks = []forge.Remark{{ID: "1", Author: "reviewer", Body: "Fix the race", Path: "main.go"}}
	for _, action := range []string{"feedback", "checks", "conflicts"} {
		w := x.request(t, "pull-request-feedback", `{"host":"github.com","repo":"team/repo","number":42,"action":"`+action+`"}`, 200)
		var out map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out["prompt"], "untrusted") || !strings.Contains(out["prompt"], "force-push") {
			t.Fatal(out)
		}
	}
	if len(x.sent) != 0 {
		t.Fatal("feedback was sent without confirmation")
	}
	for _, body := range []string{`{`, `{"number":0}`, `{"action":"unknown","number":42,"host":"github.com","repo":"team/repo"}`} {
		x.request(t, "pull-requests", body, 400)
	}
	x.request(t, "pull-requests", `{"action":"watch","number":2}`, 404)
	x.request(t, "pull-requests", `{"action":"link","number":42,"remote":"missing"}`, 502)
	x.request(t, "pull-request-feedback", `{"action":"merge"}`, 400)
	x.request(t, "pull-request-feedback", `{"action":"feedback","number":5}`, 404)
	x.target = false
	x.request(t, "pull-requests", "", 503)
}

func TestChanges(t *testing.T) {
	if news, _ := changes(nil, &Snapshot{}); len(news) != 0 {
		t.Fatal(news)
	}
	old := &Snapshot{SHA: "a", Checks: forge.CIStatePending, Remarks: map[string]string{"1": "old"}}
	next := &Snapshot{SHA: "a", Checks: forge.CIStateSuccess, Conflict: true, Remarks: map[string]string{"1": "new", "2": "same-time"}}
	news, only := changes(old, next)
	if len(news) != 3 || only {
		t.Fatal(news, only)
	}
	old.Checks = next.Checks
	old.Conflict = true
	news, only = changes(old, next)
	if len(news) != 1 || !only {
		t.Fatal(news, only)
	}
	if got := bounded("ééé\x00", 2); got != "éé…" {
		t.Fatal(got)
	}
}
