package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocmaint"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

func maintenanceStatusOf(t *testing.T, srv *Server) maintenanceStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleMaintenanceStatus(rec, httptest.NewRequest(http.MethodGet, "/api/maintenance/opencode-db", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	var st maintenanceStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestMaintenanceStatus(t *testing.T) {
	if st := maintenanceStatusOf(t, &Server{}); st.Available || st.CutoffDays != 30 {
		t.Errorf("without a DB path: %+v, want unavailable with the cutoff", st)
	}

	path := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("67"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := maintenanceStatusOf(t, (&Server{}).WithOpenCodeDBPath(path))
	if !st.Available || st.DBPath != path || st.DBBytes != 7 {
		t.Errorf("status = %+v, want available with db + wal bytes", st)
	}
	if st.Job.Steps == nil {
		t.Error("job.steps must be [] before any job ran; the UI reads its length")
	}
	if st.DumpPath != path+".ocman-diffs" || st.DumpBytes != 0 {
		t.Errorf("dump = %q (%d bytes), want the sibling path and no dump yet", st.DumpPath, st.DumpBytes)
	}
}

func TestMaintenanceActionErrors(t *testing.T) {
	post := func(srv *Server, action func(*ocmaint.Runner) error) int {
		rec := httptest.NewRecorder()
		srv.maintenanceAction(action)(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		return rec.Code
	}
	if code := post(&Server{}, (*ocmaint.Runner).Cleanup); code != http.StatusNotFound {
		t.Errorf("without maintenance: %d, want 404", code)
	}
	srv := (&Server{}).WithOpenCodeDBPath(filepath.Join(t.TempDir(), "opencode.db"))
	if code := post(srv, (*ocmaint.Runner).Restore); code != http.StatusConflict {
		t.Errorf("restore without a dump: %d, want 409", code)
	}
	if code := post(srv, (*ocmaint.Runner).DeleteDump); code != http.StatusOK {
		t.Errorf("deleting a missing dump: %d, want 200", code)
	}
	if code := post(srv, func(*ocmaint.Runner) error { return errors.New("boom") }); code != http.StatusInternalServerError {
		t.Errorf("unexpected error: %d, want 500", code)
	}
}

type countingRuntime struct {
	fakeRuntime
	launches int
}

func (c *countingRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	c.launches++
	return c.fakeRuntime.Launch(ctx, spec)
}

func TestGatedRuntimeRefusesLaunchWhileBlocked(t *testing.T) {
	inner := &countingRuntime{}
	var blocked error
	rt := gatedRuntime{Runtime: inner, blocked: func() error { return blocked }}

	blocked = errors.New("maintenance")
	if _, err := rt.Launch(context.Background(), ocruntime.LaunchSpec{}); err == nil || inner.launches != 0 {
		t.Fatalf("blocked launch: err %v, launches %d; want refused", err, inner.launches)
	}
	if err := rt.Stop(context.Background(), &ocruntime.Instance{}); err != nil {
		t.Errorf("stop must pass through while blocked: %v", err)
	}
	blocked = nil
	if _, err := rt.Launch(context.Background(), ocruntime.LaunchSpec{}); err != nil || inner.launches != 1 {
		t.Errorf("open launch: err %v, launches %d; want launched", err, inner.launches)
	}
}

func TestGateLaunchTmuxPassesThroughWithoutMaintenance(t *testing.T) {
	srv := &Server{}
	name, err := srv.gateLaunchTmux(func(context.Context, string) (string, error) { return "s", nil })(context.Background(), "/d")
	if err != nil || name != "s" {
		t.Errorf("got %q, %v; want pass-through", name, err)
	}
	if _, ok := srv.gatedRuntime().(gatedRuntime); !ok {
		t.Error("gatedRuntime should wrap even a nil runtime")
	}
}
