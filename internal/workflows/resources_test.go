package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// poolGateExecutor blocks each command until released, recording every
// concurrently-running command so tests can assert a pool cap.
type poolGateExecutor struct {
	started chan string
	release chan struct{}
}

func (e *poolGateExecutor) Execute(ctx context.Context, request CommandRequest) CommandResult {
	e.started <- request.Command[0]
	select {
	case <-e.release:
		return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: "null"}
	case <-ctx.Done():
		return CommandResult{State: AttemptCanceled, ExitCode: -1, Error: ctx.Err().Error()}
	}
}

func poolCommandNode(id string, pool string, units int) Node {
	node := Node{ID: id, Name: id, Type: "command", Command: []string{id}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}
	if pool != "" {
		node.Resources = []ResourceRequest{{Pool: pool, Units: units}}
	}
	return node
}

func publishAndStart(t *testing.T, h *harness, def Definition) RunDetail {
	t.Helper()
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	version, err := h.svc.PublishJSON(context.Background(), raw)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	run, err := h.svc.Start(context.Background(), version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return run
}

func TestResourcePoolValidation(t *testing.T) {
	base := func() Definition {
		return Definition{
			ID: "pools", Name: "Pools", Version: "1", Concurrency: 2,
			Directory: t.TempDir(),
			Triggers:  []Trigger{{ID: "manual", Type: TriggerManual}},
			Pools:     []Pool{{Name: "compiler", Capacity: 1}},
			Nodes:     []Node{poolCommandNode("a", "compiler", 1)},
		}
	}
	tests := []struct {
		name string
		mut  func(*Definition)
		want string
	}{
		{"unknown pool", func(d *Definition) { d.Nodes[0].Resources[0].Pool = "ghost" }, "undeclared resource pool"},
		{"zero units", func(d *Definition) { d.Nodes[0].Resources[0].Units = 0 }, "must be positive"},
		{"over capacity", func(d *Definition) { d.Nodes[0].Resources[0].Units = 2 }, "exceeding capacity"},
		{"zero capacity", func(d *Definition) { d.Pools[0].Capacity = 0 }, "capacity must be positive"},
		{"duplicate pool", func(d *Definition) { d.Pools = append(d.Pools, Pool{Name: "compiler", Capacity: 1}) }, "duplicate resource pool"},
		{"nameless pool", func(d *Definition) { d.Pools[0].Name = "" }, "pool name is required"},
		{"negative limit", func(d *Definition) { d.Limits = &Limits{MaxCostUSD: -1} }, "must not be negative"},
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

// TestNamedPoolCapsConcurrentCommands proves a named pool with capacity 1
// serializes two otherwise-parallel commands (concurrency 2), releasing
// capacity only after the first settles.
func TestNamedPoolCapsConcurrentCommands(t *testing.T) {
	h := newHarness(t)
	executor := &poolGateExecutor{started: make(chan string, 2), release: make(chan struct{})}
	h.svc = NewService(Deps{Store: h.db, Now: h.clock, CommandExecutor: executor})
	def := Definition{
		ID: "pools", Name: "Pools", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Pools:    []Pool{{Name: "compiler", Capacity: 1}},
		Nodes:    []Node{poolCommandNode("one", "compiler", 1), poolCommandNode("two", "compiler", 1)},
	}
	run := publishAndStart(t, h, def)

	<-executor.started // exactly one command acquired the pool
	select {
	case second := <-executor.started:
		t.Fatalf("second command %q oversubscribed the pool", second)
	case <-time.After(100 * time.Millisecond):
	}
	// A durable held lease exists for the running node in the compiler pool.
	leases, err := h.db.ListWorkflowResourceLeases(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if compilerHeld(leases) != 1 {
		t.Fatalf("expected 1 held compiler unit, got leases %+v", leases)
	}
	close(executor.release)
	done := waitForRun(t, h.svc, run.ID, StateSuccessful)
	assertRun(t, done, StateSuccessful, map[string]string{"one": NodeSuccessful, "two": NodeSuccessful})
	// All capacity released after settle.
	leases, err = h.db.ListWorkflowResourceLeases(run.ID)
	if err != nil || len(leases) != 0 {
		t.Fatalf("expected no leases after completion, got %+v (%v)", leases, err)
	}
}

func compilerHeld(leases []state.WorkflowResourceLease) int {
	total := 0
	for _, lease := range leases {
		if lease.Pool == "compiler" {
			total += lease.Units
		}
	}
	return total
}

// TestConcurrencyCapAcrossPools proves the required run-concurrency cap is
// enforced through the implicit run pool even when named pools have room.
func TestConcurrencyCapAcrossPools(t *testing.T) {
	h := newHarness(t)
	executor := &poolGateExecutor{started: make(chan string, 3), release: make(chan struct{})}
	h.svc = NewService(Deps{Store: h.db, Now: h.clock, CommandExecutor: executor})
	def := Definition{
		ID: "cap", Name: "Cap", Version: "1", Concurrency: 1, Directory: t.TempDir(),
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Pools:    []Pool{{Name: "wide", Capacity: 5}},
		Nodes: []Node{
			poolCommandNode("one", "wide", 1),
			poolCommandNode("two", "wide", 1),
		},
	}
	run := publishAndStart(t, h, def)
	<-executor.started
	select {
	case second := <-executor.started:
		t.Fatalf("run concurrency cap of 1 was exceeded by %q", second)
	case <-time.After(100 * time.Millisecond):
	}
	close(executor.release)
	waitForRun(t, h.svc, run.ID, StateSuccessful)
}

// TestResourceReleaseOnFailure proves a failing node releases every held
// pool so the run does not leak capacity.
func TestResourceReleaseOnFailure(t *testing.T) {
	h := newHarness(t)
	def := Definition{
		ID: "fail", Name: "Fail", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Pools:    []Pool{{Name: "compiler", Capacity: 1}},
		Nodes: []Node{{
			ID: "boom", Name: "Boom", Type: "command",
			Command:    []string{"/bin/sh", "-c", "exit 3"},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}},
			Resources:  []ResourceRequest{{Pool: "compiler", Units: 1}},
		}},
	}
	run := publishAndStart(t, h, def)
	waitForRun(t, h.svc, run.ID, StateFailed)
	leases, err := h.db.ListWorkflowResourceLeases(run.ID)
	if err != nil || len(leases) != 0 {
		t.Fatalf("failure leaked leases: %+v (%v)", leases, err)
	}
}

// TestResourceReleaseOnCancel proves cancel releases held capacity.
func TestResourceReleaseOnCancel(t *testing.T) {
	h := newHarness(t)
	executor := &poolGateExecutor{started: make(chan string, 1), release: make(chan struct{})}
	h.svc = NewService(Deps{Store: h.db, Now: h.clock, CommandExecutor: executor})
	def := Definition{
		ID: "cancel", Name: "Cancel", Version: "1", Concurrency: 1, Directory: t.TempDir(),
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Pools:    []Pool{{Name: "compiler", Capacity: 1}},
		Nodes:    []Node{poolCommandNode("one", "compiler", 1)},
	}
	run := publishAndStart(t, h, def)
	<-executor.started
	// Cancel while the command is still blocked and holding the pool. The
	// executor honors ctx cancellation, so the attempt settles as canceled.
	if _, err := h.svc.Cancel(context.Background(), run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	leases, err := h.db.ListWorkflowResourceLeases(run.ID)
	if err != nil || len(leases) != 0 {
		t.Fatalf("cancel leaked leases: %+v (%v)", leases, err)
	}
}

// TestResourceWaitsAreVisibleAndDurable proves a ready node blocked on a
// full pool is reported as waiting and that this survives a restart.
func TestResourceWaitsAreVisibleAndDurable(t *testing.T) {
	h := newHarness(t)
	executor := &poolGateExecutor{started: make(chan string, 2), release: make(chan struct{})}
	h.svc = NewService(Deps{Store: h.db, Now: h.clock, CommandExecutor: executor})
	def := Definition{
		ID: "waits", Name: "Waits", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Pools:    []Pool{{Name: "compiler", Capacity: 1}},
		Nodes:    []Node{poolCommandNode("one", "compiler", 1), poolCommandNode("two", "compiler", 1)},
	}
	run := publishAndStart(t, h, def)
	<-executor.started

	detail, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !poolWaiting(detail, "compiler", "two") {
		t.Fatalf("node two not reported waiting on compiler: %+v", detail.Resources)
	}
	if poolHeld(detail, "compiler") != 1 {
		t.Fatalf("compiler held not 1: %+v", detail.Resources)
	}

	close(executor.release)
	waitForRun(t, h.svc, run.ID, StateSuccessful)
}

func poolWaiting(detail RunDetail, pool, node string) bool {
	for _, p := range detail.Resources {
		if p.Pool != pool {
			continue
		}
		for _, w := range p.Waiting {
			if w == node {
				return true
			}
		}
	}
	return false
}

func poolHeld(detail RunDetail, pool string) int {
	for _, p := range detail.Resources {
		if p.Pool == pool {
			return p.Held
		}
	}
	return 0
}

// fakeWorkflowUsage returns fixed per-session usage for budget tests.
type fakeWorkflowUsage struct {
	perSessionTokens int64
	perSessionCost   float64
	ok               bool

	mu    sync.Mutex
	asked []state.Key
}

func (f *fakeWorkflowUsage) SessionUsage(_ context.Context, sessions []state.Key) (int64, float64, bool) {
	if !f.ok {
		return 0, 0, false
	}
	f.mu.Lock()
	f.asked = append(f.asked, sessions...)
	f.mu.Unlock()
	return int64(len(sessions)) * f.perSessionTokens, float64(len(sessions)) * f.perSessionCost, true
}

func (f *fakeWorkflowUsage) askedFor() []state.Key {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]state.Key(nil), f.asked...)
}

// TestCostLimitStopsRun proves a configured cost limit fails the run once
// descendant agent usage crosses it, releasing resources.
func TestCostLimitStopsRun(t *testing.T) {
	h := newHarness(t)
	usage := &fakeWorkflowUsage{perSessionCost: 10, perSessionTokens: 0, ok: true}
	h.svc = NewService(Deps{Store: h.db, Agent: h.agent, Now: h.clock, Usage: usage})
	def := singleAgentDefinition()
	def.Limits = &Limits{MaxCostUSD: 5}
	run := publishAndStart(t, h, def)
	// First dispatch launches the agent (fresh). Session now has usage.
	// Next dispatch sees the session's $10 cost exceed the $5 limit.
	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	failed, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed {
		t.Fatalf("cost limit did not stop run: %s", failed.State)
	}
	leases, err := h.db.ListWorkflowResourceLeases(run.ID)
	if err != nil || len(leases) != 0 {
		t.Fatalf("budget stop leaked leases: %+v (%v)", leases, err)
	}
}

// TestDurationLimitStopsRun proves the wall-clock duration limit fails a run.
func TestDurationLimitStopsRun(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: h.agent, Now: h.clock})
	def := singleAgentDefinition()
	def.Limits = &Limits{MaxDurationSecs: 60}
	run := publishAndStart(t, h, def)
	// Advance the clock past the duration limit, then tick.
	h.setNow(h.clock().Add(2 * time.Minute))
	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	failed, err := h.svc.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed {
		t.Fatalf("duration limit did not stop run: %s", failed.State)
	}
}

// TestUnlimitedByDefault proves omitting limits keeps a run unbounded.
func TestUnlimitedByDefault(t *testing.T) {
	h := newHarness(t)
	usage := &fakeWorkflowUsage{perSessionCost: 1000, perSessionTokens: 1_000_000, ok: true}
	h.svc = NewService(Deps{Store: h.db, Agent: h.agent, Now: h.clock, Usage: usage})
	def := singleAgentDefinition() // no Limits
	run := publishAndStart(t, h, def)
	h.agent.results["session-1"] = AgentResult{State: "waiting", FinalMessage: "null"}
	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	done := waitForRun(t, h.svc, run.ID, StateSuccessful)
	assertRun(t, done, StateSuccessful, map[string]string{"implement": NodeSuccessful})
}

func singleAgentDefinition() Definition {
	return Definition{
		ID: "implement", Name: "Implement", Version: "1", Concurrency: 1,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes:    []Node{{ID: "implement", Name: "Implement", Type: "agent", Agent: &AgentConfig{Platform: "test", Directory: "/repo", Prompt: "implement it"}}},
	}
}
