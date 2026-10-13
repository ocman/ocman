package ocmaint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// routeMux collects a module's routes on a plain mux.
type routeMux struct{ *http.ServeMux }

func (r routeMux) Handle(p string, h http.HandlerFunc) { r.HandleFunc(p, h) }
func (r routeMux) Local(p string, h http.HandlerFunc) {
	panic("maintenance guards its routes itself: " + p)
}
func (r routeMux) Setting(p string, h http.HandlerFunc)      { panic("not a setting: " + p) }
func (r routeMux) LocalSetting(p string, h http.HandlerFunc) { panic("not a setting: " + p) }

// headerGuard rejects a request lacking header with code.
func headerGuard(header string, code int) func(http.HandlerFunc) http.HandlerFunc {
	return func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(header) == "" {
				http.Error(w, header, code)
				return
			}
			h(w, r)
		}
	}
}

func mount(r *Runner) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(r, module.Guards{
		Auth:      headerGuard("X-Auth", http.StatusUnauthorized),
		Localhost: headerGuard("X-Local", http.StatusForbidden),
	}).Routes(routeMux{mux})
	return mux
}

func call(mux *http.ServeMux, method, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, h := range headers {
		req.Header.Set(h, "1")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) status {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	var st status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

const statusPath = "/api/maintenance/opencode-db"

var actionPaths = []string{statusPath + "/cleanup", statusPath + "/restore", statusPath + "/delete-dump"}

func TestModuleRoutesGuards(t *testing.T) {
	mux := mount(nil)
	for _, c := range []struct {
		method, path string
		headers      []string
		want         int
	}{
		{http.MethodPost, statusPath, []string{"X-Auth", "X-Local"}, http.StatusMethodNotAllowed},
		{http.MethodGet, statusPath, nil, http.StatusUnauthorized},
		{http.MethodGet, statusPath, []string{"X-Auth"}, http.StatusOK}, // status is not localhost-only
	} {
		if rec := call(mux, c.method, c.path, c.headers...); rec.Code != c.want {
			t.Errorf("%s %s %v = %d, want %d", c.method, c.path, c.headers, rec.Code, c.want)
		}
	}
	for _, p := range actionPaths {
		for _, c := range []struct {
			method  string
			headers []string
			want    int
		}{
			{http.MethodGet, []string{"X-Auth", "X-Local"}, http.StatusMethodNotAllowed},
			{http.MethodPost, []string{"X-Local"}, http.StatusUnauthorized},
			{http.MethodPost, []string{"X-Auth"}, http.StatusForbidden},
			{http.MethodPost, []string{"X-Auth", "X-Local"}, http.StatusNotFound}, // no runner
		} {
			if rec := call(mux, c.method, p, c.headers...); rec.Code != c.want {
				t.Errorf("%s %s %v = %d, want %d", c.method, p, c.headers, rec.Code, c.want)
			}
		}
	}
}

func TestModuleStatus(t *testing.T) {
	if st := decodeStatus(t, call(mount(nil), http.MethodGet, statusPath, "X-Auth")); st.Available || st.CutoffDays != 30 || st.Job.Steps == nil {
		t.Errorf("without a runner: %+v, want unavailable with the cutoff and [] steps", st)
	}
	path := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("67"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRunner(t, &fakeHost{}, path, 1<<50)
	st := decodeStatus(t, call(mount(r), http.MethodGet, statusPath, "X-Auth"))
	if !st.Available || st.DBPath != path || st.DBBytes != 7 {
		t.Errorf("status = %+v, want available with db + wal bytes", st)
	}
	if st.Job.Steps == nil {
		t.Error("job.steps must be [] before any job ran; the UI reads its length")
	}
	if st.DumpPath != r.DumpPath() || st.DumpBytes != 0 {
		t.Errorf("dump = %q (%d bytes), want the runner's path and no dump yet", st.DumpPath, st.DumpBytes)
	}
}

func TestModuleActionErrors(t *testing.T) {
	r := newRunner(t, &fakeHost{}, filepath.Join(t.TempDir(), "opencode.db"), 1<<50)
	m := NewModule(r, module.Guards{})
	post := func(do func(*Runner) error) int {
		rec := httptest.NewRecorder()
		m.action(do)(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		return rec.Code
	}
	mux := mount(r)
	if rec := call(mux, http.MethodPost, statusPath+"/restore", "X-Auth", "X-Local"); rec.Code != http.StatusConflict {
		t.Errorf("restore without a dump: %d, want 409", rec.Code)
	}
	if rec := call(mux, http.MethodPost, statusPath+"/delete-dump", "X-Auth", "X-Local"); rec.Code != http.StatusOK {
		t.Errorf("deleting a missing dump: %d, want 200", rec.Code)
	}
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"busy":     {ErrBusy, http.StatusConflict},
		"v2":       {fmt.Errorf("x: %w", ErrV2), http.StatusConflict},
		"shutdown": {ErrShutdown, http.StatusConflict},
		"other":    {errors.New("boom"), http.StatusInternalServerError},
	} {
		if code := post(func(*Runner) error { return c.err }); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
}

func TestModuleCleanupOnV2IsConflict(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	r := newRunner(t, &fakeHost{}, newOpenCodeDB(t), 1<<50)
	if rec := call(mount(r), http.MethodPost, statusPath+"/cleanup", "X-Auth", "X-Local"); rec.Code != http.StatusConflict {
		t.Errorf("cleanup on v2: %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// A server shutting down mid-job must wait for the job: its host callbacks
// write state.db, which closes after the workers join. The gate must reopen
// and the stopped instance relaunch; no new job may start meanwhile.
func TestModuleRunJoinsRunningJob(t *testing.T) {
	h := &fakeHost{}
	r := newRunner(t, h, newOpenCodeDB(t), 1<<50)
	stopping, release := make(chan struct{}), make(chan struct{})
	stop := r.deps.Stop
	r.deps.Stop = func(ctx context.Context, root string) error {
		close(stopping)
		<-release
		return stop(ctx, root)
	}
	m := NewModule(r, module.Guards{})
	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	<-stopping
	if m.LaunchBlocked() == nil {
		t.Fatal("launches must be blocked while the job runs")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while the job was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the job finished")
	}
	if s := r.Status(); s.Running || s.Error != "" {
		t.Errorf("job = %+v, want finished without error", s)
	}
	if m.LaunchBlocked() != nil {
		t.Error("the launch gate must reopen after shutdown")
	}
	if len(h.started) != 1 {
		t.Errorf("relaunched %v, want the stopped instance", h.started)
	}
	if err := r.Cleanup(); !errors.Is(err, ErrShutdown) {
		t.Errorf("cleanup after shutdown: %v, want ErrShutdown", err)
	}
	if err := NewModule(nil, module.Guards{}).LaunchBlocked(); err != nil {
		t.Errorf("unavailable module blocks launches: %v", err)
	}
}

func TestModuleRunWithoutRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NewModule(nil, module.Guards{}).Run(ctx) // must not block or panic
}

func TestModuleCleanupRestoreDeleteRoutes(t *testing.T) {
	path := newOpenCodeDB(t)
	r := newRunner(t, &fakeHost{}, path, 1<<50)
	mux := mount(r)
	for _, action := range []string{"cleanup", "restore", "delete-dump"} {
		rec := call(mux, http.MethodPost, statusPath+"/"+action, "X-Auth", "X-Local")
		decodeStatus(t, rec)
		if s := wait(t, r); s.Error != "" {
			t.Fatalf("%s: %s", action, s.Error)
		}
		if action == "cleanup" {
			st := decodeStatus(t, call(mux, http.MethodGet, statusPath, "X-Auth"))
			if st.DumpBytes == 0 {
				t.Fatal("cleanup must report the saved dump")
			}
		}
	}
	if _, err := os.Stat(r.DumpPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete-dump did not remove the dump: %v", err)
	}
	if got := rowData(t, path, "message", "m-old"); !strings.Contains(got, oldDiffs) {
		t.Fatalf("restore lost the archived diffs: %s", got)
	}
}
