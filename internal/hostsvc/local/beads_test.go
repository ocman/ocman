package local

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type beadsRun struct {
	out string
	err error
}

func supportedBeadsRuns(runs ...beadsRun) []beadsRun {
	return append([]beadsRun{{out: `{"version":"1.1.0"}`}}, runs...)
}

func TestSupportedBeadsVersion(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{name: "minimum", json: `{"version":"1.1.0"}`, want: true},
		{name: "new major", json: `{"version":"2.0.0"}`, want: true},
		{name: "old", json: `{"version":"1.0.9"}`},
		{name: "missing patch", json: `{"version":"1.1"}`},
		{name: "invalid major", json: `{"version":"x.1.0"}`},
		{name: "invalid minor", json: `{"version":"1.x.0"}`},
		{name: "invalid patch", json: `{"version":"1.1.x"}`},
		{name: "malformed JSON", json: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportedBeadsVersion([]byte(tt.json)); got != tt.want {
				t.Fatalf("supportedBeadsVersion(%q) = %v, want %v", tt.json, got, tt.want)
			}
		})
	}
}

type fakeBeadsRunner struct {
	pathErr error
	runs    []beadsRun
	seen    [][]string
	dirs    []string
	envs    [][]string
}

func (f *fakeBeadsRunner) LookPath(string) (string, error) {
	return "/usr/bin/bd", f.pathErr
}

func (f *fakeBeadsRunner) Run(_ context.Context, _, dir string, args, env []string) ([]byte, []byte, error) {
	f.seen = append(f.seen, append([]string(nil), args...))
	f.dirs = append(f.dirs, dir)
	f.envs = append(f.envs, append([]string(nil), env...))
	run := f.runs[len(f.seen)-1]
	return []byte(run.out), nil, run.err
}

func TestBeadsStatusUnavailableWithoutExecutable(t *testing.T) {
	runner := &fakeBeadsRunner{pathErr: errors.New("missing")}
	h := New(Deps{})
	h.beadsRunner = runner

	got, err := h.BeadsStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Available || len(runner.seen) != 0 {
		t.Fatalf("got %+v, commands %v", got, runner.seen)
	}
}

func TestBeadsStatusBuildsParentTreeWithDefaultList(t *testing.T) {
	runner := &fakeBeadsRunner{runs: supportedBeadsRuns(
		beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`},
		beadsRun{out: `[{"id":"bd-parent","title":"Parent","status":"open","priority":1,"issue_type":"epic"},{"id":"bd-child","title":"Child","status":"in_progress","priority":2,"issue_type":"task"}]`},
		beadsRun{out: `[{"issue_id":"bd-child","depends_on_id":"bd-parent","type":"parent-child"}]`},
	)}
	h := New(Deps{})
	h.beadsRunner = runner

	got, err := h.BeadsStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	want := hostsvc.BeadsStatus{Available: true, Tickets: []hostsvc.BeadsTicket{
		{ID: "bd-parent", Title: "Parent", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "bd-child", Title: "Child", Status: "in_progress", Priority: 2, IssueType: "task", ParentID: "bd-parent"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	wantCommands := [][]string{
		{"version", "--json"},
		{"--readonly", "where", "--json"},
		{"-C", "/repo", "--readonly", "list", "--json"},
		{"-C", "/repo", "--readonly", "dep", "list", "bd-parent", "bd-child", "--type", "parent-child", "--json"},
	}
	if !reflect.DeepEqual(runner.seen, wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.seen, wantCommands)
	}
	if runner.dirs[1] != "/repo" || !reflect.DeepEqual(runner.envs[1], []string{"BD_JSON_ENVELOPE=1", "BEADS_DIR=", "BEADS_DB=", "BD_DB="}) {
		t.Fatalf("where dir=%q env=%v", runner.dirs[1], runner.envs[1])
	}
}

func TestBeadsStatusReusesRecentResult(t *testing.T) {
	runs := supportedBeadsRuns(
		beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`},
		beadsRun{out: `[{"id":"bd-1","title":"One","status":"open","priority":1}]`},
	)
	runner := &fakeBeadsRunner{runs: append(runs, runs...)}
	h := New(Deps{})
	h.beadsRunner = runner

	first, err := h.BeadsStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.BeadsStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second result = %#v, want %#v", second, first)
	}
	if len(runner.seen) != 3 {
		t.Fatalf("commands = %d, want 3", len(runner.seen))
	}
}

type concurrentBeadsRunner struct {
	calls         atomic.Int32
	versionCalls  atomic.Int32
	firstStarted  chan struct{}
	secondStarted chan struct{}
	release       chan struct{}
}

func (r *concurrentBeadsRunner) LookPath(string) (string, error) { return "/usr/bin/bd", nil }

func (r *concurrentBeadsRunner) Run(_ context.Context, _ string, _ string, args, _ []string) ([]byte, []byte, error) {
	r.calls.Add(1)
	if args[0] == "version" {
		switch r.versionCalls.Add(1) {
		case 1:
			close(r.firstStarted)
			<-r.release
		case 2:
			close(r.secondStarted)
		}
		return []byte(`{"version":"1.1.0"}`), nil, nil
	}
	if args[0] == "--readonly" {
		return []byte(`{"schema_version":1,"data":{"path":"/repo/.beads"}}`), nil, nil
	}
	return []byte(`[{"id":"bd-1","title":"One","status":"open","priority":1}]`), nil, nil
}

func TestBeadsStatusCoalescesConcurrentReads(t *testing.T) {
	runner := &concurrentBeadsRunner{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		release:       make(chan struct{}),
	}
	h := New(Deps{})
	h.beadsRunner = runner
	done := make(chan error, 2)
	go func() {
		_, err := h.BeadsStatus(context.Background(), "/repo")
		done <- err
	}()
	<-runner.firstStarted
	go func() {
		_, err := h.BeadsStatus(context.Background(), "/repo")
		done <- err
	}()
	select {
	case <-runner.secondStarted:
	case <-time.After(100 * time.Millisecond):
	}
	close(runner.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := runner.calls.Load(); got != 3 {
		t.Fatalf("commands = %d, want 3", got)
	}
}

func TestBeadsStatusCanceledLeaderDoesNotCancelFollower(t *testing.T) {
	runner := &concurrentBeadsRunner{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		release:       make(chan struct{}),
	}
	h := New(Deps{})
	h.beadsRunner = runner
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := h.BeadsStatus(leaderCtx, "/repo")
		leaderDone <- err
	}()
	<-runner.firstStarted
	followerStarted := make(chan struct{})
	followerDone := make(chan error, 1)
	go func() {
		close(followerStarted)
		_, err := h.BeadsStatus(context.Background(), "/repo")
		followerDone <- err
	}()
	<-followerStarted
	cancelLeader()
	select {
	case err := <-leaderDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader err = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(runner.release)
		t.Fatal("canceled leader kept waiting for shared refresh")
	}
	close(runner.release)
	if err := <-followerDone; err != nil {
		t.Fatalf("follower err = %v", err)
	}
	if got := runner.calls.Load(); got != 3 {
		t.Fatalf("commands = %d, want 3", got)
	}
}

func TestBeadsStatusCanceledFollowerDoesNotWait(t *testing.T) {
	runner := &concurrentBeadsRunner{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		release:       make(chan struct{}),
	}
	h := New(Deps{})
	h.beadsRunner = runner
	leaderDone := make(chan error, 1)
	go func() {
		_, err := h.BeadsStatus(context.Background(), "/repo")
		leaderDone <- err
	}()
	<-runner.firstStarted
	followerCtx, cancelFollower := context.WithCancel(context.Background())
	cancelFollower()
	followerDone := make(chan error, 1)
	go func() {
		_, err := h.BeadsStatus(followerCtx, "/repo")
		followerDone <- err
	}()

	select {
	case err := <-followerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("follower err = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(runner.release)
		t.Fatal("canceled follower kept waiting for shared refresh")
	}
	close(runner.release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader err = %v", err)
	}
	if got := runner.calls.Load(); got != 3 {
		t.Fatalf("commands = %d, want 3", got)
	}
}

func TestBeadsStatusFailureStates(t *testing.T) {
	tests := []struct {
		name string
		runs []beadsRun
		want hostsvc.BeadsStatus
	}{
		{name: "unsupported version", runs: []beadsRun{{out: `{"version":"1.0.9"}`}}, want: hostsvc.BeadsStatus{}},
		{name: "missing workspace", runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"error":"no_beads_directory"}}`, err: errors.New("exit 1")}), want: hostsvc.BeadsStatus{}},
		{name: "unsupported discovery schema", runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":2,"data":{"path":"/repo/.beads"}}`}), want: hostsvc.BeadsStatus{}},
		{name: "malformed list", runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{out: `{`}), want: hostsvc.BeadsStatus{}},
		{name: "invalid ticket", runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{out: `[{"id":"bd-1","title":"Bad","status":"unknown","priority":1}]`}), want: hostsvc.BeadsStatus{}},
		{name: "list failure", runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{err: errors.New("timeout")}), want: hostsvc.BeadsStatus{Available: true, Error: "status_unavailable"}},
		{name: "dependency failure retains tickets", runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{out: `[{"id":"bd-1","title":"One","status":"open","priority":1},{"id":"bd-2","title":"Two","status":"open","priority":2}]`}, beadsRun{err: errors.New("timeout")}), want: hostsvc.BeadsStatus{Available: true, Tickets: []hostsvc.BeadsTicket{{ID: "bd-1", Title: "One", Status: "open", Priority: 1}, {ID: "bd-2", Title: "Two", Status: "open", Priority: 2}}, Error: "status_unavailable"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Deps{})
			h.beadsRunner = &fakeBeadsRunner{runs: tt.runs}
			got, err := h.BeadsStatus(context.Background(), "/repo")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

type deadlineBeadsRunner struct {
	deadlineSet bool
	calls       int
}

func (r *deadlineBeadsRunner) LookPath(string) (string, error) { return "/usr/bin/bd", nil }
func (r *deadlineBeadsRunner) Run(ctx context.Context, _, _ string, _ []string, _ []string) ([]byte, []byte, error) {
	r.calls++
	if r.calls == 1 {
		return []byte(`{"version":"1.1.0"}`), nil, nil
	}
	deadline, ok := ctx.Deadline()
	r.deadlineSet = ok && time.Until(deadline) <= beadsTimeout
	return nil, nil, context.DeadlineExceeded
}

func TestBeadsStatusBoundsCommandsWithDeadline(t *testing.T) {
	runner := &deadlineBeadsRunner{}
	h := New(Deps{})
	h.beadsRunner = runner
	_, err := h.BeadsStatus(context.Background(), "/repo")
	if !errors.Is(err, context.DeadlineExceeded) || !runner.deadlineSet {
		t.Fatalf("deadline=%v err=%v", runner.deadlineSet, err)
	}
}

func TestExecBeadsRunnerHonorsCancellation(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = (execBeadsRunner{}).Run(ctx, sleep, t.TempDir(), []string{"10"}, nil)
	if err == nil {
		t.Fatal("cancelled command unexpectedly succeeded")
	}
}

func TestExecBeadsRunnerReplacesEnvironmentOverrides(t *testing.T) {
	envPath, err := exec.LookPath("env")
	if err != nil {
		t.Skip("env is unavailable")
	}
	t.Setenv("BEADS_DIR", "/wrong/repo")
	out, _, err := (execBeadsRunner{}).Run(context.Background(), envPath, t.TempDir(), nil, []string{"BEADS_DIR="})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(out), "BEADS_DIR="); got != 1 {
		t.Fatalf("BEADS_DIR entries = %d, want 1", got)
	}
}

func TestLimitedBufferCapsOutput(t *testing.T) {
	buffer := limitedBuffer{remaining: 3}
	if n, err := buffer.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := buffer.String(); got != "abc" || !buffer.overflow {
		t.Fatalf("buffer=%q overflow=%v", got, buffer.overflow)
	}
}

func TestBeadsStatusMissingParentStaysTopLevel(t *testing.T) {
	runner := &fakeBeadsRunner{runs: supportedBeadsRuns(
		beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`},
		beadsRun{out: `[{"id":"bd-child","title":"Child","status":"deferred","priority":3},{"id":"bd-other","title":"Other","status":"open","priority":2}]`},
		beadsRun{out: `[{"issue_id":"bd-child","depends_on_id":"bd-parent","type":"parent-child"}]`},
	)}
	h := New(Deps{})
	h.beadsRunner = runner
	got, err := h.BeadsStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tickets[0].ParentID != "" {
		t.Fatalf("missing parent leaked into tree: %+v", got.Tickets[0])
	}
}

func TestBeadsStatusBreaksParentCycles(t *testing.T) {
	runner := &fakeBeadsRunner{runs: supportedBeadsRuns(
		beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`},
		beadsRun{out: `[{"id":"bd-a","title":"A","status":"open","priority":1},{"id":"bd-b","title":"B","status":"open","priority":1}]`},
		beadsRun{out: `[{"issue_id":"bd-a","depends_on_id":"bd-b","type":"parent-child"},{"issue_id":"bd-b","depends_on_id":"bd-a","type":"parent-child"}]`},
	)}
	h := New(Deps{})
	h.beadsRunner = runner
	got, err := h.BeadsStatus(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tickets[0].ParentID != "" || got.Tickets[1].ParentID != "bd-a" {
		t.Fatalf("cycle was not reduced to a tree: %+v", got.Tickets)
	}
}
