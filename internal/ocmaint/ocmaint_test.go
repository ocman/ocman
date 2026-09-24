package ocmaint

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	oldDiffs = `[{"file":"a.json","status":"deleted","patch":"-huge"}]`
	newDiffs = `[{"file":"b.go","status":"modified","patch":"+x"}]`
)

// newOpenCodeDB builds a minimal OpenCode database: an old and a recent
// session, each with a user message carrying diffs and a matching
// message.updated event, plus a compaction message whose summary is a
// boolean flag.
func newOpenCodeDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := time.Now().Add(-2 * CutoffAge).UnixMilli()
	recent := time.Now().UnixMilli()
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE session (id TEXT PRIMARY KEY, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE event (id TEXT PRIMARY KEY, aggregate_id TEXT NOT NULL, seq INTEGER NOT NULL, type TEXT NOT NULL, data TEXT NOT NULL)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO session VALUES ('old', ?), ('new', ?)`, old, recent)
	exec(`INSERT INTO message VALUES ('m-old', 'old', '{"role":"user","summary":{"title":"t","diffs":` + oldDiffs + `}}')`)
	exec(`INSERT INTO message VALUES ('m-flag', 'old', '{"role":"assistant","summary":true}')`)
	exec(`INSERT INTO message VALUES ('m-new', 'new', '{"role":"user","summary":{"diffs":` + newDiffs + `}}')`)
	exec(`INSERT INTO event VALUES ('e-old', 'old', 1, 'message.updated.1', '{"info":{"id":"m-old","summary":{"diffs":` + oldDiffs + `}}}')`)
	exec(`INSERT INTO event VALUES ('e-new', 'new', 1, 'message.updated.1', '{"info":{"id":"m-new","summary":{"diffs":` + newDiffs + `}}}')`)
	return path
}

func rowData(t *testing.T, path, table, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var data string
	if err := db.QueryRow(`SELECT data FROM `+table+` WHERE id = ?`, id).Scan(&data); err != nil {
		t.Fatal(err)
	}
	return data
}

type fakeHost struct {
	mu      sync.Mutex
	stopped []string
	started []string
	changed int
	holders []Holder
	gateErr error // Gate.Err() observed while stopping
	runner  *Runner
}

func newRunner(t *testing.T, h *fakeHost, dbPath string, free uint64) *Runner {
	t.Helper()
	r := New(Deps{
		DBPath:    dbPath,
		DumpPath:  filepath.Join(t.TempDir(), "maintenance", "opencode-diffs.db"),
		Instances: func(context.Context) ([]string, error) { return []string{"/repo"}, nil },
		Stop: func(_ context.Context, root string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.stopped = append(h.stopped, root)
			h.gateErr = h.runner.Gate.Err()
			return nil
		},
		Start: func(_ context.Context, root string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.started = append(h.started, root)
			return nil
		},
		Holders:    func(context.Context, string) ([]Holder, error) { return h.holders, nil },
		FreeBytes:  func(string) (uint64, error) { return free, nil },
		OnChanged:  func() { h.mu.Lock(); h.changed++; h.mu.Unlock() },
		HolderWait: 10 * time.Millisecond,
	})
	h.runner = r
	return r
}

func wait(t *testing.T, r *Runner) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := r.Status(); !s.Running {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Status{}
}

func TestCleanupDumpsStripsAndRestores(t *testing.T) {
	path := newOpenCodeDB(t)
	origOld := rowData(t, path, "message", "m-old")
	origEvent := rowData(t, path, "event", "e-old")
	h := &fakeHost{}
	r := newRunner(t, h, path, 1<<50)

	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	s := wait(t, r)
	if s.Error != "" {
		t.Fatalf("cleanup failed: %s (%+v)", s.Error, s.Steps)
	}
	for _, st := range s.Steps {
		if st.State != "done" {
			t.Errorf("step %q = %s, want done", st.Name, st.State)
		}
	}
	if got := rowData(t, path, "message", "m-old"); strings.Contains(got, "diffs") || !strings.Contains(got, `"title":"t"`) {
		t.Errorf("old message = %s, want summary without diffs", got)
	}
	if got := rowData(t, path, "event", "e-old"); strings.Contains(got, "diffs") {
		t.Errorf("old event = %s, want no diffs", got)
	}
	if got := rowData(t, path, "message", "m-new"); !strings.Contains(got, "diffs") {
		t.Errorf("recent message lost its diffs: %s", got)
	}
	if got := rowData(t, path, "event", "e-new"); !strings.Contains(got, "diffs") {
		t.Errorf("recent event lost its diffs: %s", got)
	}
	if got := rowData(t, path, "message", "m-flag"); !strings.Contains(got, `"summary":true`) {
		t.Errorf("compaction flag lost: %s", got)
	}
	if _, err := os.Stat(r.backupPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup should be deleted after success, stat err = %v", err)
	}
	if !reflect.DeepEqual(h.stopped, []string{"/repo"}) || !reflect.DeepEqual(h.started, []string{"/repo"}) {
		t.Errorf("stopped %v started %v, want /repo each", h.stopped, h.started)
	}
	if h.gateErr == nil {
		t.Error("launch gate should be closed while the job runs")
	}
	if r.Gate.Err() != nil {
		t.Error("launch gate should reopen after the job")
	}
	if h.changed == 0 {
		t.Error("OnChanged not called")
	}

	if err := r.Restore(); err != nil {
		t.Fatal(err)
	}
	if s := wait(t, r); s.Error != "" {
		t.Fatalf("restore failed: %s", s.Error)
	}
	if got := rowData(t, path, "message", "m-old"); !sameJSON(t, got, origOld) {
		t.Errorf("restored message = %s, want %s", got, origOld)
	}
	if got := rowData(t, path, "event", "e-old"); !sameJSON(t, got, origEvent) {
		t.Errorf("restored event = %s, want %s", got, origEvent)
	}

	if err := r.DeleteDump(); err != nil {
		t.Fatal(err)
	}
	if err := r.Restore(); err == nil {
		t.Error("restore without a dump should fail")
	}
}

// sameJSON compares two JSON documents via SQLite's canonical form.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var same bool
	if err := db.QueryRow(`SELECT json(?) = json(?)`, a, b).Scan(&same); err != nil {
		t.Fatal(err)
	}
	return same
}

func TestCleanupRefusesWhileAnotherProcessHoldsTheDB(t *testing.T) {
	path := newOpenCodeDB(t)
	before := rowData(t, path, "message", "m-old")
	h := &fakeHost{holders: []Holder{{PID: 42, Command: "opencode"}}}
	r := newRunner(t, h, path, 1<<50)

	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	s := wait(t, r)
	if !strings.Contains(s.Error, "opencode (pid 42)") {
		t.Fatalf("error = %q, want the holder named", s.Error)
	}
	if got := rowData(t, path, "message", "m-old"); got != before {
		t.Errorf("database changed despite refusal: %s", got)
	}
	if !reflect.DeepEqual(h.started, []string{"/repo"}) {
		t.Errorf("stopped instances must be relaunched after a refusal, started %v", h.started)
	}
	if last := s.Steps[len(s.Steps)-1]; last.State != "done" {
		t.Errorf("relaunch step = %s, want done", last.State)
	}
	if s.Steps[2].State != "skipped" {
		t.Errorf("backup step = %s, want skipped", s.Steps[2].State)
	}
}

func TestCleanupRefusesWithoutDiskSpace(t *testing.T) {
	path := newOpenCodeDB(t)
	h := &fakeHost{}
	r := newRunner(t, h, path, 0)

	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	s := wait(t, r)
	if !strings.Contains(s.Error, "free") {
		t.Fatalf("error = %q, want a disk space error", s.Error)
	}
	if len(h.stopped) != 0 {
		t.Errorf("nothing should be stopped before the space check passes, stopped %v", h.stopped)
	}
}

func TestOneJobAtATime(t *testing.T) {
	path := newOpenCodeDB(t)
	release := make(chan struct{})
	h := &fakeHost{}
	r := newRunner(t, h, path, 1<<50)
	r.deps.Instances = func(context.Context) ([]string, error) { <-release; return nil, nil }

	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := r.Cleanup(); !errors.Is(err, ErrBusy) {
		t.Errorf("second cleanup err = %v, want ErrBusy", err)
	}
	if err := r.DeleteDump(); !errors.Is(err, ErrBusy) {
		t.Errorf("delete dump while running err = %v, want ErrBusy", err)
	}
	close(release)
	wait(t, r)
}

func TestLsofHoldersExcludesSelf(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not installed")
	}
	path := newOpenCodeDB(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	holders, err := LsofHolders(context.Background(), path)
	if err != nil || len(holders) != 0 {
		t.Errorf("holders = %v, %v; want none (only this process holds it)", holders, err)
	}
	if holders, err := LsofHolders(context.Background(), filepath.Join(t.TempDir(), "missing.db")); err != nil || holders != nil {
		t.Errorf("missing db: %v, %v; want none", holders, err)
	}
	if free, err := FreeBytes(filepath.Dir(path)); err != nil || free == 0 {
		t.Errorf("FreeBytes = %d, %v", free, err)
	}
}

func TestParseLsof(t *testing.T) {
	out := "p100\ncopencode\np7\ncocman\np200\ncnode\np100\ncopencode\n"
	got := parseLsof(out, 7)
	want := []Holder{{PID: 100, Command: "opencode"}, {PID: 200, Command: "node"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLsof = %+v, want %+v", got, want)
	}
}
