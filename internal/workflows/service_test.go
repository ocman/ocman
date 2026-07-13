package workflows

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

const sequentialApprovals = `{
	"id":"release",
	"name":"Release",
	"version":"2026.07",
	"concurrency":1,
	"nodes":[
		{"id":"review","name":"Review","type":"approval"},
		{"id":"ship","name":"Ship","type":"approval"}
	],
	"dependencies":[{"from":"review","to":"ship"}]
}`

type harness struct {
	t    *testing.T
	path string
	db   *state.DB
	now  time.Time
	svc  *Service
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:    t,
		path: filepath.Join(t.TempDir(), "state.db"),
		now:  time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC),
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
	h.svc = NewService(Deps{Store: db, Now: func() time.Time { return h.now }})
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
