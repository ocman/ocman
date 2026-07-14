package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/loops"
	"github.com/NoUseFreak/ocman/internal/state"
)

const sequentialApprovals = `{
	"id":"release",
	"name":"Release",
	"version":"2026.07",
	"concurrency":1,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[
		{"id":"review","name":"Review","type":"approval"},
		{"id":"ship","name":"Ship","type":"approval"}
	],
	"dependencies":[{"from":"review","to":"ship"}]
}`

const approvalThenAgents = `{
	"id":"implement",
	"name":"Implement",
	"version":"1",
	"concurrency":1,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[
		{"id":"approve","name":"Approve","type":"approval"},
		{"id":"implement","name":"Implement","type":"agent","agent":{"platform":"test","directory":"/repo","prompt":"implement it","sessionAffinity":"work","collectors":[
			{"name":"message","type":"final-message"},
			{"name":"patch","type":"diff"},
			{"name":"notes","type":"file","path":"notes.txt"},
			{"name":"result","type":"json-file","path":"result.json"}
		]}},
		{"id":"review","name":"Review","type":"agent","agent":{"platform":"test","directory":"/repo","prompt":"review it","sessionAffinity":"work"}}
	],
	"dependencies":[{"from":"approve","to":"implement"},{"from":"implement","to":"review"}]
}`

const singleAgent = `{
	"id":"implement","name":"Implement","version":"1","concurrency":1,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[{"id":"implement","name":"Implement","type":"agent","agent":{"platform":"test","directory":"/repo","prompt":"implement it"}}],
	"dependencies":[]
}`

type fakeAgentExecutor struct {
	starts   []AgentRequest
	results  map[string]AgentResult
	canceled []AgentSession
	startErr error
}

func (f *fakeAgentExecutor) Start(_ context.Context, req AgentRequest) (AgentSession, error) {
	f.starts = append(f.starts, req)
	if f.startErr != nil {
		return AgentSession{}, f.startErr
	}
	id := req.SessionID
	if id == "" {
		id = "session-1"
	}
	return AgentSession{ID: id, Platform: req.Platform, State: "busy"}, nil
}

func (f *fakeAgentExecutor) Inspect(_ context.Context, session AgentSession, _ []Collector) (AgentResult, error) {
	if result, ok := f.results[session.ID]; ok {
		return result, nil
	}
	return AgentResult{State: "busy"}, nil
}

func (f *fakeAgentExecutor) Cancel(_ context.Context, session AgentSession) error {
	f.canceled = append(f.canceled, session)
	return nil
}

type harness struct {
	t              *testing.T
	path           string
	blobDir        string
	db             *state.DB
	now            time.Time
	forge          *workflowFakeForge
	status         *workflowFakeStatus
	triggerChanges int
	svc            *Service
	agent          *fakeAgentExecutor
	blobs          *BlobStore
	secrets        map[string]string
}

type workflowFakeForge struct {
	mu    sync.Mutex
	state loops.PRState
	err   error
}

func (f *workflowFakeForge) PollPR(context.Context, string, int) (loops.PRState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.err
}

type workflowFakeStatus struct {
	mu      sync.Mutex
	running map[string]bool
}

func (f *workflowFakeStatus) TurnRunning(_ context.Context, _, sessionID string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	running, ok := f.running[sessionID]
	return running, ok
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		t:       t,
		path:    filepath.Join(dir, "state.db"),
		blobDir: filepath.Join(dir, "artifacts"),
		now:     time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC),
		forge:   &workflowFakeForge{},
		status:  &workflowFakeStatus{running: map[string]bool{}},
		agent:   &fakeAgentExecutor{results: map[string]AgentResult{}},
		secrets: map[string]string{},
	}
	h.open()
	t.Cleanup(func() {
		if h.db != nil {
			_ = h.db.Close()
		}
	})
	return h
}

func (h *harness) open() {
	h.t.Helper()
	db, err := state.Open(h.path)
	if err != nil {
		h.t.Fatalf("open state DB: %v", err)
	}
	h.db = db
	h.blobs = NewBlobStore(h.blobDir)
	h.svc = NewService(Deps{Store: db, Agent: h.agent, Blobs: h.blobs, ResolveSecret: func(env string) string { return h.secrets[env] }, Now: func() time.Time { return h.now }, Forge: h.forge, Status: h.status, NotifyTrigger: func() { h.triggerChanges++ }})
}

func (h *harness) restart() {
	h.t.Helper()
	if err := h.db.Close(); err != nil {
		h.t.Fatalf("close state DB: %v", err)
	}
	h.db = nil
	h.open()
}

func (h *harness) advance() { h.now = h.now.Add(time.Minute) }

func TestSequentialApprovalRunPersistsAcrossRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if version.WorkflowID != "release" || version.Revision != 1 {
		t.Fatalf("unexpected version: %+v", version)
	}

	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	assertRun(t, run, StateActive, map[string]string{"review": NodeReady, "ship": NodePending})
	if len(run.Nodes[0].Attempts) != 1 || run.Nodes[0].Attempts[0].State != AttemptWaiting {
		t.Fatalf("first approval attempt not waiting: %+v", run.Nodes[0].Attempts)
	}

	h.restart()
	restored, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	assertRun(t, restored, StateActive, map[string]string{"review": NodeReady, "ship": NodePending})

	h.advance()
	if _, err := h.svc.Approve(ctx, run.ID, "review"); err != nil {
		t.Fatalf("approve review: %v", err)
	}
	afterFirst, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get after first approval: %v", err)
	}
	assertRun(t, afterFirst, StateActive, map[string]string{"review": NodeSuccessful, "ship": NodeReady})
	if len(afterFirst.Nodes[0].Attempts) != 1 || afterFirst.Nodes[0].Attempts[0].CompletedAt != h.now.UnixMilli() {
		t.Fatalf("first attempt history not completed durably: %+v", afterFirst.Nodes[0].Attempts)
	}
	if len(afterFirst.Nodes[1].Attempts) != 1 || afterFirst.Nodes[1].Attempts[0].State != AttemptWaiting {
		t.Fatalf("second approval attempt not waiting: %+v", afterFirst.Nodes[1].Attempts)
	}

	h.advance()
	completed, err := h.svc.Approve(ctx, run.ID, "ship")
	if err != nil {
		t.Fatalf("approve ship: %v", err)
	}
	assertRun(t, completed, StateSuccessful, map[string]string{"review": NodeSuccessful, "ship": NodeSuccessful})
	if completed.VersionID != version.ID || completed.CompletedAt != h.now.UnixMilli() {
		t.Fatalf("run is not pinned/completed: %+v", completed)
	}
}

func TestPublishCreatesImmutableVersions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	v1, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	edited := strings.Replace(sequentialApprovals, `"version":"2026.07"`, `"version":"2026.08"`, 1)
	v2, err := h.svc.PublishJSON(ctx, []byte(edited))
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if v1.ID == v2.ID || v2.Revision != 2 {
		t.Fatalf("edit did not create a new immutable version: v1=%+v v2=%+v", v1, v2)
	}
	stored, err := h.svc.GetVersion(ctx, v1.ID)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if stored.Definition.Version != "2026.07" {
		t.Fatalf("v1 changed after v2 publish: %+v", stored.Definition)
	}
	editedName := strings.Replace(edited, `"name":"Release"`, `"name":"Renamed"`, 1)
	if _, err := h.svc.PublishJSON(ctx, []byte(editedName)); err != nil {
		t.Fatalf("publish renamed version: %v", err)
	}
	stored, err = h.svc.GetVersion(ctx, v1.ID)
	if err != nil || stored.Name != "Release" {
		t.Fatalf("v1 name changed after later rename: %+v, %v", stored, err)
	}
}

func TestPublishValidation(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"missing concurrency", strings.Replace(sequentialApprovals, `"concurrency":1,`, "", 1), "concurrency must be positive"},
		{"zero concurrency", strings.Replace(sequentialApprovals, `"concurrency":1`, `"concurrency":0`, 1), "concurrency must be positive"},
		{"duplicate node", strings.Replace(sequentialApprovals, `"id":"ship"`, `"id":"review"`, 1), `duplicate node "review"`},
		{"missing source", strings.Replace(sequentialApprovals, `"from":"review"`, `"from":"missing"`, 1), `missing node "missing"`},
		{"missing target", strings.Replace(sequentialApprovals, `"to":"ship"`, `"to":"missing"`, 1), `missing node "missing"`},
		{"self dependency", strings.Replace(sequentialApprovals, `"to":"ship"`, `"to":"review"`, 1), "self dependency"},
		{"cycle", strings.Replace(sequentialApprovals, `{"from":"review","to":"ship"}`, `{"from":"review","to":"ship"},{"from":"ship","to":"review"}`, 1), "cycle"},
		{"malformed dependency", strings.Replace(sequentialApprovals, `"from":"review"`, `"from":""`, 1), "dependency endpoints are required"},
		{"unsafe collector path", strings.Replace(singleAgent, `"prompt":"implement it"`, `"prompt":"implement it","collectors":[{"name":"result","type":"file","path":"../result.json"}]`, 1), "safe relative path"},
		{"missing triggers", strings.Replace(sequentialApprovals, `"triggers":[{"id":"manual","type":"manual"}],`, "", 1), "at least one trigger"},
		{"duplicate trigger", strings.Replace(sequentialApprovals, `{"id":"manual","type":"manual"}`, `{"id":"manual","type":"manual"},{"id":"manual","type":"manual"}`, 1), `duplicate trigger "manual"`},
		{"multiple manual triggers", strings.Replace(sequentialApprovals, `{"id":"manual","type":"manual"}`, `{"id":"manual","type":"manual"},{"id":"other","type":"manual"}`, 1), "only one manual trigger"},
		{"invalid overlap", strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"manual","overlap":"later"`, 1), "invalid overlap"},
		{"invalid interval", strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"interval"`, 1), "intervalSeconds must be positive"},
		{"invalid cron", strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"cron","cron":"nope"`, 1), "invalid cron"},
		{"invalid PR", strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"pr"`, 1), "requires prNumber and directory"},
		{"invalid completion", strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"turn_completion"`, 1), "requires sessionId"},
		{"unsupported trigger", strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"webhook"`, 1), "unsupported type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.svc.PublishJSON(context.Background(), []byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestRunPauseCancelAndInvalidApproval(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Approve(ctx, run.ID, "ship"); err == nil {
		t.Fatal("approved a pending node")
	}
	paused, err := h.svc.Pause(ctx, run.ID)
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	assertRun(t, paused, StatePaused, map[string]string{"review": NodeReady, "ship": NodePending})
	if _, err := h.svc.Approve(ctx, run.ID, "review"); err == nil {
		t.Fatal("approved a paused run")
	}
	canceled, err := h.svc.Cancel(ctx, run.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	assertRun(t, canceled, StateCanceled, map[string]string{"review": NodeCanceled, "ship": NodeCanceled})
}

func TestSequentialCommandsCollectDeclaredOutputs(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("collected file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("note.txt diff=unsafe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	textconv := filepath.Join(dir, "textconv.sh")
	if err := os.WriteFile(textconv, []byte("#!/bin/sh\ntouch textconv-ran\ncat \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := runTestGit(dir, "init"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := runTestGit(dir, "config", "diff.unsafe.textconv", textconv); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	if out, err := runTestGit(dir, "add", "note.txt", ".gitattributes"); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("collected file\nchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	definition := commandDefinition(t, dir, []Node{
		{
			ID: "first", Name: "First", Type: "command",
			Command:     []string{"/bin/sh", "-c", `printf '%s' "$GREETING"`},
			Environment: map[string]string{"GREETING": "hello"},
			Permission:  []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}},
			Outputs:     []Collector{{Name: "stdout", Type: "text"}, {Name: "json", Type: "json_file", Path: "result.json"}, {Name: "file", Type: "file", Path: "note.txt"}, {Name: "diff", Type: "git_diff"}},
		},
		{
			ID: "second", Name: "Second", Type: "command",
			Command:    []string{"/usr/bin/printf", "done"},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "/usr/bin/printf *", Action: "allow"}},
		},
	}, []Dependency{{From: "first", To: "second"}})
	version, err := h.svc.PublishJSON(context.Background(), definition)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run = waitForRun(t, h.svc, run.ID, StateSuccessful)
	assertRun(t, run, StateSuccessful, map[string]string{"first": NodeSuccessful, "second": NodeSuccessful})
	first := run.Nodes[0].Attempts[0]
	if first.State != AttemptSuccessful || first.ExitCode == nil || *first.ExitCode != 0 || first.Stdout != "hello" || first.CompletedAt == 0 {
		t.Fatalf("unexpected first attempt: %+v", first)
	}
	if got := string(first.Outputs["json"]); got != `"{\"ok\":true}"` {
		t.Fatalf("json output: %s", got)
	}
	if got := string(first.Outputs["file"]); got != `"collected file\nchanged"` {
		t.Fatalf("file output: %s", got)
	}
	if got := string(first.Outputs["diff"]); !strings.Contains(got, "+changed") {
		t.Fatalf("git diff output: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "textconv-ran")); !os.IsNotExist(err) {
		t.Fatalf("git diff collector executed repository textconv: %v", err)
	}
	h.restart()
	restored, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil || string(restored.Nodes[0].Attempts[0].Outputs["json"]) != `"{\"ok\":true}"` {
		t.Fatalf("command attempt did not survive restart: %+v, %v", restored, err)
	}
}

func runTestGit(directory string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
	cmd.Env = gitexec.CleanEnv()
	return cmd.CombinedOutput()
}

func TestCommandPermissionIsDefaultDeny(t *testing.T) {
	h := newHarness(t)
	definition := commandDefinition(t, t.TempDir(), []Node{{
		ID: "denied", Name: "Denied", Type: "command", Command: []string{"/usr/bin/touch", "should-not-exist"},
		Permission: []PermissionRule{{Permission: "bash", Pattern: "/usr/bin/printf *", Action: "allow"}},
	}}, nil)
	version, err := h.svc.PublishJSON(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = waitForRun(t, h.svc, run.ID, StateFailed)
	attempt := run.Nodes[0].Attempts[0]
	if attempt.State != AttemptDenied || !strings.Contains(attempt.Error, "permission denied") {
		t.Fatalf("unexpected denied attempt: %+v", attempt)
	}
}

func TestCommandCollectorCountIsBounded(t *testing.T) {
	h := newHarness(t)
	node := Node{ID: "many", Name: "Many", Type: "command", Command: []string{"/usr/bin/true"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}
	for i := 0; i < 33; i++ {
		node.Outputs = append(node.Outputs, Collector{Name: fmt.Sprintf("output-%d", i), Type: "text"})
	}
	_, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), []Node{node}, nil))
	if err == nil || !strings.Contains(err.Error(), "at most 32 collectors") {
		t.Fatalf("expected collector bound error, got %v", err)
	}
}

func TestCommandPermissionUsesLastMatchingRuleAndExplicitEnvironment(t *testing.T) {
	h := newHarness(t)
	t.Setenv("UNDECLARED_SECRET", "must-not-leak")
	node := Node{
		ID: "safe", Name: "Safe", Type: "command",
		Command:     []string{"/bin/sh", "-c", `test -z "$UNDECLARED_SECRET" && test "$DECLARED" = visible`},
		Environment: map[string]string{"DECLARED": "visible"},
		Permission: []PermissionRule{
			{Permission: "bash", Pattern: "*", Action: "allow"},
			{Permission: "bash", Pattern: "/bin/sh -c *", Action: "deny"},
		},
	}
	version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), []Node{node}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = waitForRun(t, h.svc, run.ID, StateFailed)
	if run.Nodes[0].Attempts[0].State != AttemptDenied {
		t.Fatalf("last matching deny did not win: %+v", run.Nodes[0].Attempts[0])
	}

	node.Permission = []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}}
	version, err = h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), []Node{node}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err = h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = waitForRun(t, h.svc, run.ID, StateSuccessful, StateFailed)
	if run.State != StateSuccessful {
		t.Fatalf("environment was not scoped as declared: %+v", run.Nodes[0].Attempts[0])
	}
}

func TestCollectorCannotFollowSymlinkOutsideWorkflowDirectory(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "result.txt")); err != nil {
		t.Fatal(err)
	}
	node := Node{ID: "collect", Name: "Collect", Type: "command", Command: []string{"/usr/bin/true"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}, Outputs: []Collector{{Name: "file", Type: "file", Path: "result.txt"}}}
	version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, dir, []Node{node}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	run = waitForRun(t, h.svc, run.ID, StateFailed)
	if !strings.Contains(run.Nodes[0].Attempts[0].Error, "escapes workflow directory") {
		t.Fatalf("unexpected collector outcome: %+v", run.Nodes[0].Attempts[0])
	}
}

func TestCommandOutcomesAndBoundedLogs(t *testing.T) {
	tests := []struct {
		name      string
		command   []string
		outputs   []Collector
		wantState string
		wantError string
	}{
		{name: "nonzero exit", command: []string{"/bin/sh", "-c", "printf problem >&2; exit 7"}, wantState: AttemptFailed},
		{name: "executor error", command: []string{"/definitely/missing"}, wantState: AttemptErrored},
		{name: "missing collector", command: []string{"/usr/bin/true"}, outputs: []Collector{{Name: "missing", Type: "file", Path: "missing.txt"}}, wantState: AttemptFailed, wantError: "collecting missing"},
		{name: "malformed json", command: []string{"/bin/sh", "-c", "printf nope > result.json"}, outputs: []Collector{{Name: "json", Type: "json_file", Path: "result.json"}}, wantState: AttemptFailed, wantError: "invalid JSON"},
		{name: "bounded output", command: []string{"/bin/sh", "-c", "yes x | head -c 100000"}, wantState: AttemptSuccessful},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			node := Node{ID: "command", Name: "Command", Type: "command", Command: tt.command, Outputs: tt.outputs, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}
			version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), []Node{node}, nil))
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.Start(context.Background(), version.ID)
			if err != nil {
				t.Fatal(err)
			}
			run = waitForRun(t, h.svc, run.ID, StateFailed, StateSuccessful)
			attempt := run.Nodes[0].Attempts[0]
			if attempt.State != tt.wantState || !strings.Contains(attempt.Error, tt.wantError) {
				t.Fatalf("unexpected attempt: %+v", attempt)
			}
			if tt.name == "nonzero exit" && (attempt.ExitCode == nil || *attempt.ExitCode != 7 || attempt.Stderr != "problem") {
				t.Fatalf("exit outcome missing details: %+v", attempt)
			}
			if tt.name == "bounded output" && (len(attempt.Stdout) > maxCommandOutput+64 || !attempt.StdoutTruncated) {
				t.Fatalf("stdout was not bounded: len=%d truncated=%v", len(attempt.Stdout), attempt.StdoutTruncated)
			}
		})
	}
}

func TestCancelTerminatesCommandProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are Unix-only")
	}
	h := newHarness(t)
	dir := t.TempDir()
	command := fmt.Sprintf("sleep 30 & echo $! > %s; wait", filepath.Join(dir, "child.pid"))
	node := Node{ID: "long", Name: "Long", Type: "command", Command: []string{"/bin/sh", "-c", command}, Permission: []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}}}
	version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, dir, []Node{node}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(dir, "child.pid")
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, readErr := os.ReadFile(pidPath)
		if readErr == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child process did not start")
	}
	canceled, err := h.svc.Cancel(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.State != StateCanceled || canceled.Nodes[0].Attempts[0].State != AttemptCanceled {
		t.Fatalf("unexpected canceled run: %+v", canceled)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("child process %d survived cancellation", pid)
	}
}

func TestPauseLetsRunningCommandSettleAndCommandCannotBeApproved(t *testing.T) {
	h := newHarness(t)
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	h.svc = NewService(Deps{Store: h.db, Now: func() time.Time { return h.now }, CommandExecutor: executor})
	node := Node{ID: "command", Name: "Command", Type: "command", Command: []string{"/usr/bin/true"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}
	version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), []Node{node}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-executor.started
	if _, err := h.svc.Approve(context.Background(), run.ID, "command"); err == nil || !strings.Contains(err.Error(), "not an approval") {
		t.Fatalf("command node was approved: %v", err)
	}
	if _, err := h.svc.Pause(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	close(executor.release)
	settled := waitForRun(t, h.svc, run.ID, StateSuccessful)
	if settled.Nodes[0].Attempts[0].State != AttemptSuccessful {
		t.Fatalf("paused command did not settle: %+v", settled)
	}
}

func TestTickFailsCommandInterruptedByRestart(t *testing.T) {
	h := newHarness(t)
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	h.svc = NewService(Deps{Store: h.db, Now: func() time.Time { return h.now }, CommandExecutor: executor, Agent: h.agent})
	node := Node{ID: "command", Name: "Command", Type: "command", Command: []string{"/usr/bin/true"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}
	version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), []Node{node}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-executor.started
	h.svc = NewService(Deps{Store: h.db, Now: func() time.Time { return h.now }, Agent: h.agent})
	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed || failed.Nodes[0].Attempts[0].State != AttemptErrored || !strings.Contains(failed.Nodes[0].Attempts[0].Error, "server restart") {
		t.Fatalf("interrupted command not failed: %+v", failed)
	}
	close(executor.release)
	<-executor.done
}

func TestTickFailsAgentLaunchInterruptedByRestart(t *testing.T) {
	h := newHarness(t)
	version, err := h.svc.PublishJSON(context.Background(), []byte(singleAgent))
	if err != nil {
		t.Fatal(err)
	}
	run := state.WorkflowRun{
		ID: "interrupted-agent", WorkflowID: version.WorkflowID, VersionID: version.ID,
		State: StateActive, CreatedAt: h.now.UnixMilli(), UpdatedAt: h.now.UnixMilli(),
		Nodes: []state.WorkflowNodeRun{{NodeID: "implement", Type: "agent", State: NodeReady}},
	}
	if err := h.db.InsertWorkflowRun(run); err != nil {
		t.Fatal(err)
	}
	stored, err := h.db.GetWorkflowRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := stored.Nodes[0].Attempts[0].ID
	if claimed, err := h.db.ClaimWorkflowAgentAttempt(run.ID, "implement", attemptID, "", "/repo", []state.WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 1}}, nil, h.now.UnixMilli()); err != nil || !claimed {
		t.Fatalf("claim interrupted agent: claimed=%v err=%v", claimed, err)
	}
	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed || failed.Nodes[0].Attempts[0].State != AttemptFailed || !strings.Contains(failed.Nodes[0].Attempts[0].Error, "server restart") {
		t.Fatalf("interrupted agent launch not failed: %+v", failed)
	}
	// Restart reconciliation must release the capacity the interrupted
	// attempt held so the run does not leak run-concurrency.
	leases, err := h.db.ListWorkflowResourceLeases(run.ID)
	if err != nil || len(leases) != 0 {
		t.Fatalf("interrupted attempt leaked resource leases: %+v (%v)", leases, err)
	}
}

func TestCommandConcurrencyCapAndFailureCancelSiblings(t *testing.T) {
	t.Run("cap", func(t *testing.T) {
		h := newHarness(t)
		executor := &gatedExecutor{started: make(chan string, 2), release: make(chan struct{})}
		h.svc = NewService(Deps{Store: h.db, Now: func() time.Time { return h.now }, CommandExecutor: executor})
		nodes := []Node{
			{ID: "one", Name: "One", Type: "command", Command: []string{"one"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "two", Name: "Two", Type: "command", Command: []string{"two"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		}
		version, err := h.svc.PublishJSON(context.Background(), commandDefinition(t, t.TempDir(), nodes, nil))
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.svc.Start(context.Background(), version.ID)
		if err != nil {
			t.Fatal(err)
		}
		<-executor.started
		select {
		case second := <-executor.started:
			t.Fatalf("second command %q exceeded concurrency cap", second)
		case <-time.After(100 * time.Millisecond):
		}
		close(executor.release)
		waitForRun(t, h.svc, run.ID, StateSuccessful)
	})

	t.Run("failure cancels sibling", func(t *testing.T) {
		h := newHarness(t)
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "sibling.pid")
		nodes := []Node{
			{ID: "fail", Name: "Fail", Type: "command", Command: []string{"/bin/sh", "-c", "sleep .2; exit 9"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "sibling", Name: "Sibling", Type: "command", Command: []string{"/bin/sh", "-c", fmt.Sprintf("sleep 30 & echo $! > %s; wait", pidPath)}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		}
		definition := Definition{ID: "parallel", Name: "Parallel", Version: "1", Concurrency: 2, Directory: dir, Triggers: []Trigger{{ID: "manual", Type: TriggerManual}}, Nodes: nodes}
		raw, _ := json.Marshal(definition)
		version, err := h.svc.PublishJSON(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.svc.Start(context.Background(), version.ID)
		if err != nil {
			t.Fatal(err)
		}
		var pid int
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			if value, readErr := os.ReadFile(pidPath); readErr == nil {
				pid, _ = strconv.Atoi(strings.TrimSpace(string(value)))
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		failed := waitForRun(t, h.svc, run.ID, StateFailed)
		if failed.Nodes[1].Attempts[0].State != AttemptCanceled {
			t.Fatalf("sibling attempt was not canceled: %+v", failed.Nodes[1])
		}
		if pid == 0 {
			t.Fatal("sibling child did not start")
		}
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("sibling child %d survived failure", pid)
		}
	})

	t.Run("simultaneous failures do not dispatch queued command", func(t *testing.T) {
		h := newHarness(t)
		executor := &failingGateExecutor{started: make(chan string, 3), release: make(chan struct{})}
		h.svc = NewService(Deps{Store: h.db, Now: func() time.Time { return h.now }, CommandExecutor: executor})
		nodes := []Node{
			{ID: "one", Name: "One", Type: "command", Command: []string{"one"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "two", Name: "Two", Type: "command", Command: []string{"two"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "queued", Name: "Queued", Type: "command", Command: []string{"queued"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		}
		raw, _ := json.Marshal(Definition{ID: "failures", Name: "Failures", Version: "1", Concurrency: 2, Directory: t.TempDir(), Triggers: []Trigger{{ID: "manual", Type: TriggerManual}}, Nodes: nodes})
		version, err := h.svc.PublishJSON(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.svc.Start(context.Background(), version.ID)
		if err != nil {
			t.Fatal(err)
		}
		<-executor.started
		<-executor.started
		close(executor.release)
		failed := waitForRun(t, h.svc, run.ID, StateFailed)
		if failed.Nodes[2].Attempts[0].State != AttemptCanceled {
			t.Fatalf("queued attempt was not canceled: %+v", failed.Nodes[2])
		}
		select {
		case command := <-executor.started:
			t.Fatalf("queued command %q was dispatched", command)
		default:
		}
	})
}

type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

type gatedExecutor struct {
	started chan string
	release chan struct{}
}

type failingGateExecutor struct {
	started chan string
	release chan struct{}
}

func (e *failingGateExecutor) Execute(_ context.Context, request CommandRequest) CommandResult {
	e.started <- request.Command[0]
	<-e.release
	return CommandResult{State: AttemptFailed, ExitCode: 1, Error: "failed", Outputs: map[string]string{}}
}

func (e *gatedExecutor) Execute(_ context.Context, request CommandRequest) CommandResult {
	e.started <- request.Command[0]
	<-e.release
	exitCode := 0
	return CommandResult{State: AttemptSuccessful, ExitCode: exitCode, Outputs: map[string]string{}}
}

func (e *blockingExecutor) Execute(context.Context, CommandRequest) CommandResult {
	close(e.started)
	<-e.release
	if e.done != nil {
		close(e.done)
	}
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Outputs: map[string]string{}}
}

func commandDefinition(t *testing.T, dir string, nodes []Node, dependencies []Dependency) []byte {
	t.Helper()
	raw, err := json.Marshal(Definition{ID: "commands", Name: "Commands", Version: "1", Concurrency: 1, Directory: dir, Triggers: []Trigger{{ID: "manual", Type: TriggerManual}}, Nodes: nodes, Dependencies: dependencies})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func waitForRun(t *testing.T, svc *Service, id string, states ...string) RunDetail {
	t.Helper()
	wanted := make(map[string]bool, len(states))
	for _, state := range states {
		wanted[state] = true
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := svc.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if wanted[run.State] {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach %v", id, states)
	return RunDetail{}
}

func TestAgentRunFreshAffinityCollectorsAndIdleCompletion(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	version, err := h.svc.PublishJSON(ctx, []byte(approvalThenAgents))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := h.svc.Approve(ctx, run.ID, "approve"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(h.agent.starts) != 1 || h.agent.starts[0].SessionID != "" {
		t.Fatalf("first attempt did not launch fresh: %+v", h.agent.starts)
	}
	launched, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := launched.Nodes[1].Attempts[0]
	if attempt.SessionID != "session-1" || attempt.Platform != "test" || attempt.SessionState != "busy" {
		t.Fatalf("missing attempt/session link: %+v", attempt)
	}

	h.agent.results["session-1"] = AgentResult{State: "waiting", Outputs: map[string]json.RawMessage{
		"message": json.RawMessage(`"finished"`),
		"patch":   json.RawMessage(`{"files":[{"path":"main.go"}]}`),
		"notes":   json.RawMessage(`"review notes"`),
		"result":  json.RawMessage(`{"ok":true}`),
	}}
	h.advance()
	if err := h.svc.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(h.agent.starts) != 2 || h.agent.starts[1].SessionID != "session-1" {
		t.Fatalf("explicit affinity did not reuse session: %+v", h.agent.starts)
	}

	h.agent.results["session-1"] = AgentResult{State: "done"}
	h.advance()
	if err := h.svc.Tick(ctx); err != nil {
		t.Fatalf("final tick: %v", err)
	}
	completed, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRun(t, completed, StateSuccessful, map[string]string{"approve": NodeSuccessful, "implement": NodeSuccessful, "review": NodeSuccessful})
	outputs := completed.Nodes[1].Attempts[0].Outputs
	if string(outputs["message"]) != `"finished"` || string(outputs["result"]) != `{"ok":true}` {
		t.Fatalf("collectors not persisted: %s", outputs)
	}
}

func TestAgentTerminalErrorAndCancellation(t *testing.T) {
	t.Run("launch error", func(t *testing.T) {
		h := newHarness(t)
		h.agent.startErr = errors.New("create failed")
		version, err := h.svc.PublishJSON(t.Context(), []byte(singleAgent))
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.svc.Start(t.Context(), version.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.State != StateFailed || run.Nodes[0].Attempts[0].Error != "create failed" {
			t.Fatalf("launch failure not persisted: %+v", run)
		}
	})

	t.Run("terminal error", func(t *testing.T) {
		h := newHarness(t)
		version, err := h.svc.PublishJSON(t.Context(), []byte(singleAgent))
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.svc.Start(t.Context(), version.ID)
		if err != nil {
			t.Fatal(err)
		}
		h.agent.results["session-1"] = AgentResult{State: "error", Error: "agent failed"}
		if err := h.svc.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		failed, err := h.svc.GetRun(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if failed.State != StateFailed || failed.Nodes[0].Attempts[0].Error != "agent failed" {
			t.Fatalf("terminal error not recorded: %+v", failed)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		h := newHarness(t)
		version, err := h.svc.PublishJSON(t.Context(), []byte(singleAgent))
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.svc.Start(t.Context(), version.ID)
		if err != nil {
			t.Fatal(err)
		}
		canceled, err := h.svc.Cancel(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(h.agent.canceled) != 1 || h.agent.canceled[0].ID != "session-1" {
			t.Fatalf("session cancellation not delegated: %+v", h.agent.canceled)
		}
		if canceled.Nodes[0].Attempts[0].State != AttemptCanceled {
			t.Fatalf("attempt cancellation not recorded: %+v", canceled.Nodes[0].Attempts[0])
		}
	})
}

func TestValidateDoesNotPublishAndResume(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	definition, err := h.svc.ValidateJSON(ctx, []byte(sequentialApprovals))
	if err != nil || definition.ID != "release" {
		t.Fatalf("validate: %+v, %v", definition, err)
	}
	versions, err := h.svc.ListVersions(ctx)
	if err != nil || len(versions) != 0 {
		t.Fatalf("validation persisted a version: %+v, %v", versions, err)
	}
	version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	// Start accepts either an immutable version ID or the workflow's active version.
	run, err := h.svc.Start(ctx, version.WorkflowID)
	if err != nil || run.VersionID != version.ID {
		t.Fatalf("start active version: %+v, %v", run, err)
	}
	if _, err := h.svc.Pause(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := h.svc.Resume(ctx, run.ID)
	if err != nil || resumed.State != StateActive {
		t.Fatalf("resume: %+v, %v", resumed, err)
	}
}

func TestTriggerSnapshotIsImmutableAndPinned(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	v1, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(ctx, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger == nil || run.Trigger.Type != TriggerManual || run.Trigger.Overlap != OverlapSkip || run.Trigger.VersionID != v1.ID {
		t.Fatalf("run did not pin defaulted manual trigger: %+v", run.Trigger)
	}
	repeated, err := h.svc.Start(ctx, v1.ID)
	if err != nil || repeated.ID != run.ID {
		t.Fatalf("manual skip did not return active run: %+v, %v", repeated, err)
	}
	runs, _ := h.svc.ListRuns(ctx)
	status, _ := h.svc.GetTrigger(ctx, v1.ID, "manual")
	if len(runs) != 1 || status.LastDecision != DecisionSkipped {
		t.Fatalf("manual overlap was not skipped: runs=%d status=%+v", len(runs), status)
	}
	edited := strings.Replace(sequentialApprovals, `"type":"manual"`, `"type":"manual","overlap":"parallel"`, 1)
	if _, err := h.svc.PublishJSON(ctx, []byte(edited)); err != nil {
		t.Fatal(err)
	}
	restored, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Trigger.Overlap != OverlapSkip || restored.VersionID != v1.ID {
		t.Fatalf("stored trigger snapshot changed: %+v", restored)
	}
}

func TestManualOverlapAcrossVersionsReturnsBlockingRun(t *testing.T) {
	h := newHarness(t)
	v1, err := h.svc.PublishJSON(context.Background(), []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	active, err := h.svc.Start(context.Background(), v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := h.svc.PublishJSON(context.Background(), []byte(strings.Replace(sequentialApprovals, `"version":"2026.07"`, `"version":"2026.08"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := h.svc.Start(context.Background(), v2.ID)
	if err != nil || blocked.ID != active.ID {
		t.Fatalf("new version did not return blocking run: %+v, %v", blocked, err)
	}
	status, _ := h.svc.GetTrigger(context.Background(), v2.ID, "manual")
	if status.LastDecision != DecisionSkipped || status.LastRunID != active.ID {
		t.Fatalf("cross-version skip not recorded: %+v", status)
	}
}

func TestTriggerOverlapPoliciesAndQueuedRestart(t *testing.T) {
	for _, policy := range []string{OverlapSkip, OverlapQueue, OverlapParallel} {
		t.Run(policy, func(t *testing.T) {
			h := newHarness(t)
			definition := strings.Replace(sequentialApprovals,
				`{"id":"manual","type":"manual"}`,
				`{"id":"timer","type":"interval","intervalSeconds":60,"overlap":"`+policy+`"}`, 1)
			version, err := h.svc.PublishJSON(context.Background(), []byte(definition))
			if err != nil {
				t.Fatal(err)
			}
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.now = h.now.Add(time.Minute)
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			status, err := h.svc.GetTrigger(context.Background(), version.ID, "timer")
			if err != nil {
				t.Fatal(err)
			}
			runs, err := h.svc.ListRuns(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			switch policy {
			case OverlapSkip:
				if len(runs) != 1 || status.LastDecision != DecisionSkipped {
					t.Fatalf("skip: runs=%d status=%+v", len(runs), status)
				}
			case OverlapParallel:
				if len(runs) != 2 || status.LastDecision != DecisionStarted {
					t.Fatalf("parallel: runs=%d status=%+v", len(runs), status)
				}
			case OverlapQueue:
				if len(runs) != 1 || status.Queued != 1 || status.LastDecision != DecisionQueued {
					t.Fatalf("queue: runs=%d status=%+v", len(runs), status)
				}
				manualOnly := strings.Replace(sequentialApprovals, `"version":"2026.07"`, `"version":"2026.08"`, 1)
				if _, err := h.svc.PublishJSON(context.Background(), []byte(manualOnly)); err != nil {
					t.Fatal(err)
				}
				h.restart()
				if _, err := h.svc.Cancel(context.Background(), runs[0].ID); err != nil {
					t.Fatal(err)
				}
				if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
					t.Fatal(err)
				}
				runs, _ = h.svc.ListRuns(context.Background())
				status, _ = h.svc.GetTrigger(context.Background(), version.ID, "timer")
				if len(runs) != 2 || status.Queued != 0 || runs[0].VersionID != version.ID || runs[0].Trigger == nil || runs[0].Trigger.FiredAt != h.now.UnixMilli() {
					t.Fatalf("queued firing did not survive restart: runs=%+v status=%+v", runs, status)
				}
			}
		})
	}
}

func TestEvaluateTriggers_SkipsLoopCompatibilityWorkflows(t *testing.T) {
	h := newHarness(t)
	definition := strings.Replace(sequentialApprovals,
		`"version":"2026.07",`,
		`"version":"2026.07","loopCompat":{"loopId":"loop_legacy"},`, 1)
	definition = strings.Replace(definition,
		`{"id":"manual","type":"manual"}`,
		`{"id":"timer","type":"interval","intervalSeconds":60}`, 1)
	if _, err := h.svc.PublishJSON(context.Background(), []byte(definition)); err != nil {
		t.Fatal(err)
	}

	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := h.svc.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("loop compatibility workflow was dispatched: %+v", runs)
	}
}

func TestTick_CancelsActiveLoopCompatibilityWorkflowRun(t *testing.T) {
	h := newHarness(t)
	definition := strings.Replace(sequentialApprovals,
		`"version":"2026.07",`,
		`"version":"2026.07","loopCompat":{"loopId":"loop_legacy"},`, 1)
	definition = strings.Replace(definition,
		`{"id":"manual","type":"manual"}`,
		`{"id":"timer","type":"interval","intervalSeconds":60}`, 1)
	version, err := h.svc.PublishJSON(context.Background(), []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := h.db.GetWorkflowVersion(version.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := h.svc.newRun(*stored, TriggerSnapshot{Trigger: Trigger{ID: "timer", Type: TriggerInterval}, VersionID: version.ID, FiredAt: h.now.UnixMilli()})
	if err := h.db.InsertWorkflowRun(run); err != nil {
		t.Fatal(err)
	}

	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCanceled {
		t.Fatalf("active compatibility run state = %q, want %q", got.State, StateCanceled)
	}
}

func TestCronDoesNotBackfireHistoricalSlots(t *testing.T) {
	h := newHarness(t)
	definition := strings.Replace(sequentialApprovals,
		`{"id":"manual","type":"manual"}`,
		`{"id":"cron","type":"cron","cron":"0 1 * * *"}`, 1)
	version, err := h.svc.PublishJSON(context.Background(), []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ := h.svc.ListRuns(context.Background())
	if len(runs) != 0 {
		t.Fatalf("cron backfired: %+v", runs)
	}
	status, _ := h.svc.GetTrigger(context.Background(), version.ID, "cron")
	if status.NextCheckAt <= h.now.UnixMilli() {
		t.Fatalf("missing future cron check: %+v", status)
	}
	if h.triggerChanges == 0 {
		t.Fatal("cron baseline did not notify trigger observers")
	}
	h.restart()
	h.now = time.Date(2026, 7, 14, 1, 5, 0, 0, time.UTC)
	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ = h.svc.ListRuns(context.Background())
	if len(runs) != 1 {
		t.Fatalf("cron slot after persisted baseline was lost or duplicated: %+v", runs)
	}
}

func TestPRTriggerPersistsDetectionState(t *testing.T) {
	h := newHarness(t)
	definition := strings.Replace(sequentialApprovals,
		`{"id":"manual","type":"manual"}`,
		`{"id":"pr","type":"pr","prNumber":322,"directory":"/repo","overlap":"parallel"}`, 1)
	version, err := h.svc.PublishJSON(context.Background(), []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	h.forge.state.HeadSHA = "a"
	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.now = h.now.Add(30 * time.Second)
	h.forge.state.HeadSHA = "b"
	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ := h.svc.ListRuns(context.Background())
	status, _ := h.svc.GetTrigger(context.Background(), version.ID, "pr")
	if len(runs) != 1 || status.LastFiredAt != h.now.UnixMilli() || status.LastDecision != DecisionStarted {
		t.Fatalf("PR event was lost or duplicated: runs=%+v status=%+v", runs, status)
	}
}

func TestTriggerErrorDoesNotStarveOtherWorkflows(t *testing.T) {
	h := newHarness(t)
	h.forge.err = errors.New("forge unavailable")
	definition := strings.Replace(sequentialApprovals,
		`{"id":"manual","type":"manual"}`,
		`{"id":"pr","type":"pr","prNumber":322,"directory":"/repo"},{"id":"timer","type":"interval","intervalSeconds":60}`, 1)
	if _, err := h.svc.PublishJSON(context.Background(), []byte(definition)); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.EvaluateTriggers(context.Background()); err == nil {
		t.Fatal("expected forge error")
	}
	runs, err := h.svc.ListRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Trigger == nil || runs[0].Trigger.ID != "timer" {
		t.Fatalf("forge error starved interval trigger: %+v", runs)
	}
}

func TestCompletionTriggersFireOncePerIdleEdge(t *testing.T) {
	for _, triggerType := range []string{TriggerChildCompletion, TriggerTurnCompletion} {
		t.Run(triggerType, func(t *testing.T) {
			h := newHarness(t)
			h.status.running["session-1"] = true
			definition := strings.Replace(sequentialApprovals,
				`{"id":"manual","type":"manual"}`,
				`{"id":"done","type":"`+triggerType+`","sessionId":"session-1","overlap":"parallel"}`, 1)
			if _, err := h.svc.PublishJSON(context.Background(), []byte(definition)); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.status.running["session-1"] = false
			h.now = h.now.Add(5 * time.Second)
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			runs, _ := h.svc.ListRuns(context.Background())
			if len(runs) != 1 {
				t.Fatalf("idle state double-fired: %+v", runs)
			}
			h.status.running["session-1"] = true
			h.now = h.now.Add(5 * time.Second)
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.status.running["session-1"] = false
			h.now = h.now.Add(5 * time.Second)
			if err := h.svc.EvaluateTriggers(context.Background()); err != nil {
				t.Fatal(err)
			}
			runs, _ = h.svc.ListRuns(context.Background())
			if len(runs) != 2 {
				t.Fatalf("second idle edge did not fire: %+v", runs)
			}
		})
	}
}

func assertRun(t *testing.T, run RunDetail, state string, nodes map[string]string) {
	t.Helper()
	if run.State != state {
		t.Fatalf("run state: want %s, got %s", state, run.State)
	}
	if len(run.Nodes) != len(nodes) {
		t.Fatalf("node count: want %d, got %d", len(nodes), len(run.Nodes))
	}
	for _, node := range run.Nodes {
		if want := nodes[node.NodeID]; node.State != want {
			t.Fatalf("node %s: want %s, got %s", node.NodeID, want, node.State)
		}
	}
}
