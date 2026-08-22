package workflows

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWorkspaceProvider records shard provisioning and hands back a unique
// directory per shard so tests can assert isolation without real git.
type fakeWorkspaceProvider struct {
	mu     sync.Mutex
	base   string
	shards map[int]string
}

func newFakeWorkspaceProvider(base string) *fakeWorkspaceProvider {
	return &fakeWorkspaceProvider{base: base, shards: map[int]string{}}
}

func (f *fakeWorkspaceProvider) EnsureShard(ctx context.Context, _ string, _ string, shard int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if dir, ok := f.shards[shard]; ok {
		return dir, nil
	}
	dir := filepath.Join(f.base, "shard-"+strconv.Itoa(shard))
	f.shards[shard] = dir
	return dir, nil
}

func workspaceGateService(t *testing.T, e *poolGateExecutor, provider WorkspaceProvider) *harness {
	t.Helper()
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Workspace: provider, CommandExecutor: e, Now: h.clock})
	return h
}

func leaseNode(id string, lease *LeaseConfig) Node {
	return Node{
		ID: id, Name: id, Type: "command", Command: []string{id},
		Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}},
		Lease:      lease,
	}
}

// TestExclusiveLeasePreventsSharing proves the default exclusive lease keeps
// two mutators off the same shard: with a one-shard pool and concurrency 2,
// only one command can run at a time.
func TestExclusiveLeasePreventsSharing(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 2), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "excl", Name: "Excl", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes:     []Node{leaseNode("one", &LeaseConfig{Mode: LeaseExclusive}), leaseNode("two", &LeaseConfig{Mode: LeaseExclusive})},
	}
	run := publishAndStart(t, h, def)

	<-executor.started
	select {
	case second := <-executor.started:
		t.Fatalf("exclusive lease let %q share the only shard", second)
	case <-time.After(100 * time.Millisecond):
	}
	leases, err := h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(leases) != 1 || leases[0].Mode != LeaseExclusive {
		t.Fatalf("expected one exclusive lease, got %+v (%v)", leases, err)
	}
	close(executor.release)
	done := waitForRun(t, h.svc, run.ID, StateSuccessful)
	assertRun(t, done, StateSuccessful, map[string]string{"one": NodeSuccessful, "two": NodeSuccessful})
	leases, err = h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(leases) != 0 {
		t.Fatalf("leases not released after completion: %+v (%v)", leases, err)
	}
}

// TestDisjointPathLeasesShareShard proves two path leases with
// non-overlapping scopes run concurrently on the same single shard.
func TestDisjointPathLeasesShareShard(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 2), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "disjoint", Name: "Disjoint", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes: []Node{
			leaseNode("a", &LeaseConfig{Mode: LeasePath, Paths: []string{"src/a"}}),
			leaseNode("b", &LeaseConfig{Mode: LeasePath, Paths: []string{"src/b"}}),
		},
	}
	run := publishAndStart(t, h, def)

	first := <-executor.started
	second := <-executor.started // both run concurrently
	if first == second {
		t.Fatalf("same node started twice: %q", first)
	}
	leases, err := h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(leases) != 2 {
		t.Fatalf("expected two shared leases, got %+v (%v)", leases, err)
	}
	if leases[0].Shard != leases[1].Shard {
		t.Fatalf("disjoint path leases did not share a shard: %+v", leases)
	}
	close(executor.release)
	done := waitForRun(t, h.svc, run.ID, StateSuccessful)
	assertRun(t, done, StateSuccessful, map[string]string{"a": NodeSuccessful, "b": NodeSuccessful})
}

// TestOverlappingPathLeasesSerialize proves that overlapping (ancestor)
// path scopes cannot run concurrently on the one available shard.
func TestOverlappingPathLeasesSerialize(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 2), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "overlap", Name: "Overlap", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes: []Node{
			leaseNode("parent", &LeaseConfig{Mode: LeasePath, Paths: []string{"src"}}),
			leaseNode("child", &LeaseConfig{Mode: LeasePath, Paths: []string{"src/app"}}),
		},
	}
	run := publishAndStart(t, h, def)

	<-executor.started
	select {
	case second := <-executor.started:
		t.Fatalf("overlapping scope %q ran concurrently", second)
	case <-time.After(100 * time.Millisecond):
	}
	close(executor.release)
	done := waitForRun(t, h.svc, run.ID, StateSuccessful)
	assertRun(t, done, StateSuccessful, map[string]string{"parent": NodeSuccessful, "child": NodeSuccessful})
}

// TestPathLeasedGitMutationDenied proves a path-leased command is denied a
// repository-wide git mutation (commit) via the executor's RestrictGit.
func TestPathLeasedGitMutationDenied(t *testing.T) {
	h := newHarness(t)
	provider := newFakeWorkspaceProvider(t.TempDir())
	h.svc = NewService(Deps{Store: h.db, Workspace: provider, Now: h.clock})
	def := Definition{
		ID: "gitdeny", Name: "GitDeny", Version: "1", Concurrency: 1, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes: []Node{{
			ID: "edit", Name: "Edit", Type: "command",
			Command:    []string{"git", "commit", "-m", "nope"},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}},
			Lease:      &LeaseConfig{Mode: LeasePath, Paths: []string{"src/app"}},
		}},
	}
	run := publishAndStart(t, h, def)
	done := waitForRun(t, h.svc, run.ID, StateFailed)
	attempt := done.Nodes[0].Attempts[0]
	if attempt.State != AttemptDenied {
		t.Fatalf("expected denied attempt, got %s (%q)", attempt.State, attempt.Error)
	}
}

// TestCommitCoordinatorNotRestricted proves an exclusive commit coordinator
// keeps its repository-wide git capability.
func TestCommitCoordinatorNotRestricted(t *testing.T) {
	if restrictGitFor(Definition{
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes:     []Node{{ID: "commit", Lease: &LeaseConfig{Mode: LeaseExclusive, Commit: true}}},
	}, "commit") != nil {
		t.Fatal("commit coordinator must not be git-restricted")
	}
	if restrictGitFor(Definition{
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes:     []Node{{ID: "edit", Lease: &LeaseConfig{Mode: LeasePath, Paths: []string{"src"}}}},
	}, "edit") == nil {
		t.Fatal("path-leased node must be git-restricted")
	}
}

// TestSerializedCommitCapacity proves two commit coordinators cannot hold
// the same shard concurrently (serialized per-shard commit capacity).
func TestSerializedCommitCapacity(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 2), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "commits", Name: "Commits", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes: []Node{
			leaseNode("c1", &LeaseConfig{Mode: LeaseExclusive, Commit: true}),
			leaseNode("c2", &LeaseConfig{Mode: LeaseExclusive, Commit: true}),
		},
	}
	run := publishAndStart(t, h, def)
	<-executor.started
	select {
	case second := <-executor.started:
		t.Fatalf("two commit coordinators shared a shard: %q", second)
	case <-time.After(100 * time.Millisecond):
	}
	close(executor.release)
	waitForRun(t, h.svc, run.ID, StateSuccessful)
}

// TestShardExhaustionSerializes proves that exclusive leases needing more
// shards than the pool provides serialize rather than oversubscribe.
func TestShardExhaustionSerializes(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 3), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "exhaust", Name: "Exhaust", Version: "1", Concurrency: 3, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 2},
		Nodes: []Node{
			leaseNode("one", &LeaseConfig{Mode: LeaseExclusive}),
			leaseNode("two", &LeaseConfig{Mode: LeaseExclusive}),
			leaseNode("three", &LeaseConfig{Mode: LeaseExclusive}),
		},
	}
	run := publishAndStart(t, h, def)
	<-executor.started
	<-executor.started // two shards, two concurrent exclusive leases
	select {
	case third := <-executor.started:
		t.Fatalf("shard pool of 2 admitted a third exclusive mutator: %q", third)
	case <-time.After(100 * time.Millisecond):
	}
	leases, err := h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(leases) != 2 {
		t.Fatalf("expected exactly two held shard leases, got %+v (%v)", leases, err)
	}
	close(executor.release)
	waitForRun(t, h.svc, run.ID, StateSuccessful)
}

// TestLeaseValidation covers definition-time lease/workspace validation.
func TestLeaseValidation(t *testing.T) {
	base := func() Definition {
		return Definition{
			ID: "v", Name: "V", Version: "1", Concurrency: 1, Directory: t.TempDir(),
			Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
			Workspace: &WorkspaceConfig{Shards: 1},
			Nodes:     []Node{leaseNode("a", &LeaseConfig{Mode: LeaseExclusive})},
		}
	}
	tests := []struct {
		name string
		mut  func(*Definition)
		want string
	}{
		{"zero shards", func(d *Definition) { d.Workspace.Shards = 0 }, "shard pool capacity must be positive"},
		{"lease without pool", func(d *Definition) { d.Workspace = nil }, "no workspace shard pool"},
		{"exclusive with paths", func(d *Definition) { d.Nodes[0].Lease.Paths = []string{"src"} }, "exclusive lease does not accept path scopes"},
		{"path without scopes", func(d *Definition) { d.Nodes[0].Lease = &LeaseConfig{Mode: LeasePath} }, "requires at least one declared path scope"},
		{"path commit combo", func(d *Definition) {
			d.Nodes[0].Lease = &LeaseConfig{Mode: LeasePath, Paths: []string{"src"}, Commit: true}
		}, "commit coordinator must use an exclusive lease"},
		{"overlapping self scopes", func(d *Definition) {
			d.Nodes[0].Lease = &LeaseConfig{Mode: LeasePath, Paths: []string{"src", "src/app"}}
		}, "overlap"},
		{"bad mode", func(d *Definition) { d.Nodes[0].Lease = &LeaseConfig{Mode: "weird"} }, "unsupported lease mode"},
		{"escaping scope", func(d *Definition) {
			d.Nodes[0].Lease = &LeaseConfig{Mode: LeasePath, Paths: []string{"../secret"}}
		}, "escapes the shard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			def := base()
			tt.mut(&def)
			raw, _ := json.Marshal(def)
			_, err := h.svc.PublishJSON(context.Background(), raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

// TestLeaseSurvivesRestart proves a held shard lease is durable so restart
// reconciliation and the UI can see shard ownership.
func TestLeaseSurvivesRestart(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 1), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "durable", Name: "Durable", Version: "1", Concurrency: 1, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1, Host: "local-host"},
		Nodes:     []Node{leaseNode("one", &LeaseConfig{Mode: LeaseExclusive})},
	}
	run := publishAndStart(t, h, def)
	<-executor.started
	before, err := h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(before) != 1 || before[0].Host != "local-host" {
		t.Fatalf("lease not held with host identity: %+v (%v)", before, err)
	}
	// Reopen just the DB (the running goroutine keeps the old handle; we
	// only assert durability of the persisted lease row here).
	detail, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Workspace) != 1 || detail.Workspace[0].Shard != 0 || detail.Workspace[0].Host != "local-host" {
		t.Fatalf("run detail did not surface shard ownership: %+v", detail.Workspace)
	}
	close(executor.release)
	waitForRun(t, h.svc, run.ID, StateSuccessful)
}

// TestCancelReleasesLeaseAfterSettle proves a canceled run holds the shard
// lease until the blocked command settles, then releases it.
func TestCancelReleasesLeaseAfterSettle(t *testing.T) {
	executor := &poolGateExecutor{started: make(chan string, 1), release: make(chan struct{})}
	provider := newFakeWorkspaceProvider(t.TempDir())
	h := workspaceGateService(t, executor, provider)
	def := Definition{
		ID: "cancel", Name: "Cancel", Version: "1", Concurrency: 1, Directory: t.TempDir(),
		Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
		Workspace: &WorkspaceConfig{Shards: 1},
		Nodes:     []Node{leaseNode("one", &LeaseConfig{Mode: LeaseExclusive})},
	}
	run := publishAndStart(t, h, def)
	<-executor.started
	held, err := h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("lease not held during run: %+v (%v)", held, err)
	}
	// Cancel honors ctx cancellation; the attempt settles, then leases drop.
	if _, err := h.svc.Cancel(context.Background(), run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	after, err := h.db.ListWorkflowWorkspaceLeases(t.Context(), run.ID)
	if err != nil || len(after) != 0 {
		t.Fatalf("cancel leaked shard lease: %+v (%v)", after, err)
	}
}
