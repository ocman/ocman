package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// definitionWith builds a minimal valid definition and applies mutate, so
// each validation case only states the one thing it breaks.
func definitionWith(directory string, mutate func(*Definition)) Definition {
	def := Definition{
		ID:          "wf",
		Name:        "WF",
		Version:     "1",
		Concurrency: 1,
		Directory:   directory,
		Triggers:    []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes:       []Node{{ID: "a", Name: "A", Type: "approval"}},
	}
	mutate(&def)
	return def
}

func TestValidateDefinitionRejectsMalformedWorkflows(t *testing.T) {
	dir := t.TempDir()
	approvals := func(ids ...string) []Node {
		out := make([]Node, 0, len(ids))
		for _, id := range ids {
			out = append(out, Node{ID: id, Name: strings.ToUpper(id), Type: "approval"})
		}
		return out
	}
	tests := []struct {
		name   string
		want   string
		mutate func(*Definition)
	}{
		{"missing id", "id, name, and version are required", func(d *Definition) { d.ID = "" }},
		{"missing name", "id, name, and version are required", func(d *Definition) { d.Name = "" }},
		{"missing version", "id, name, and version are required", func(d *Definition) { d.Version = "" }},
		{"zero concurrency", "concurrency must be positive", func(d *Definition) { d.Concurrency = 0 }},
		{"no nodes", "at least one node is required", func(d *Definition) { d.Nodes = nil }},
		{"no triggers", "at least one trigger is required", func(d *Definition) { d.Triggers = nil }},
		{"secret without env", "secret name and env are required", func(d *Definition) {
			d.Secrets = []SecretRef{{Name: "token"}}
		}},
		{"secret with invalid env", `references invalid env var "1BAD"`, func(d *Definition) {
			d.Secrets = []SecretRef{{Name: "token", Env: "1BAD"}}
		}},
		{"duplicate secret", `duplicate secret "token"`, func(d *Definition) {
			d.Secrets = []SecretRef{{Name: "token", Env: "A"}, {Name: "token", Env: "B"}}
		}},
		{"pool without name", "resource pool name is required", func(d *Definition) {
			d.Pools = []Pool{{Capacity: 1}}
		}},
		{"pool without capacity", `resource pool "build" capacity must be positive`, func(d *Definition) {
			d.Pools = []Pool{{Name: "build"}}
		}},
		{"duplicate pool", `duplicate resource pool "build"`, func(d *Definition) {
			d.Pools = []Pool{{Name: "build", Capacity: 1}, {Name: "build", Capacity: 2}}
		}},
		{"negative limits", "limits must not be negative", func(d *Definition) {
			d.Limits = &Limits{MaxCostUSD: -1}
		}},
		{"zero workspace shards", "workspace shard pool capacity must be positive", func(d *Definition) {
			d.Workspace = &WorkspaceConfig{Shards: 0, Repo: dir}
		}},
		{"trigger without id", "trigger id is required", func(d *Definition) {
			d.Triggers = []Trigger{{Type: TriggerManual}}
		}},
		{"duplicate trigger", `duplicate trigger "manual"`, func(d *Definition) {
			d.Triggers = []Trigger{{ID: "manual", Type: TriggerManual}, {ID: "manual", Type: TriggerInterval, IntervalSeconds: 60}}
		}},
		{"two manual triggers", "only one manual trigger is supported", func(d *Definition) {
			d.Triggers = []Trigger{{ID: "one", Type: TriggerManual}, {ID: "two", Type: TriggerManual}}
		}},
		{"node without name", "node id, name, and type are required", func(d *Definition) {
			d.Nodes = []Node{{ID: "a", Type: "approval"}}
		}},
		{"unsupported node type", `unsupported node type "shrug"`, func(d *Definition) {
			d.Nodes = []Node{{ID: "a", Name: "A", Type: "shrug"}}
		}},
		{"invalid command node", `node "a": command is required`, func(d *Definition) {
			d.Nodes = []Node{{ID: "a", Name: "A", Type: "command"}}
		}},
		{"agent node without directory", `agent node "a" requires directory and prompt`, func(d *Definition) {
			d.Nodes = []Node{{ID: "a", Name: "A", Type: "agent", Agent: &AgentConfig{Prompt: "go"}}}
		}},
		{"agent node with invalid schema", `agent node "a" has invalid outputSchema`, func(d *Definition) {
			d.Nodes = []Node{{ID: "a", Name: "A", Type: "agent", Agent: &AgentConfig{
				Directory: dir, Prompt: "go", OutputSchema: map[string]any{"type": "not-a-type"},
			}}}
		}},
		{"repeat without attempts", `node "a" repeat maxAttempts must be positive`, func(d *Definition) {
			d.Nodes[0].Repeat = &RepeatConfig{Until: "true"}
		}},
		{"repeat with invalid condition", `node "a" repeat:`, func(d *Definition) {
			d.Nodes[0].Repeat = &RepeatConfig{Until: "!!!", MaxAttempts: 2}
		}},
		{"undeclared pool request", `node "a" requests undeclared resource pool "build"`, func(d *Definition) {
			d.Nodes[0].Resources = []ResourceRequest{{Pool: "build", Units: 1}}
		}},
		{"zero resource units", `node "a" resource units for pool "build" must be positive`, func(d *Definition) {
			d.Pools = []Pool{{Name: "build", Capacity: 2}}
			d.Nodes[0].Resources = []ResourceRequest{{Pool: "build", Units: 0}}
		}},
		{"resource units above capacity", `exceeding capacity 2`, func(d *Definition) {
			d.Pools = []Pool{{Name: "build", Capacity: 2}}
			d.Nodes[0].Resources = []ResourceRequest{{Pool: "build", Units: 3}}
		}},
		{"pool requested twice", `node "a" requests pool "build" more than once`, func(d *Definition) {
			d.Pools = []Pool{{Name: "build", Capacity: 2}}
			d.Nodes[0].Resources = []ResourceRequest{{Pool: "build", Units: 1}, {Pool: "build", Units: 1}}
		}},
		{"lease without workspace pool", `node "a": declares a workspace lease`, func(d *Definition) {
			d.Nodes[0].Lease = &LeaseConfig{Mode: LeaseExclusive}
		}},
		{"duplicate node", `duplicate node "a"`, func(d *Definition) {
			d.Nodes = approvals("a", "a")
		}},
		{"dependency without endpoints", "dependency endpoints are required", func(d *Definition) {
			d.Nodes = approvals("a", "b")
			d.Dependencies = []Dependency{{From: "a"}}
		}},
		{"self dependency", `self dependency for node "a"`, func(d *Definition) {
			d.Dependencies = []Dependency{{From: "a", To: "a"}}
		}},
		{"dependency from missing node", `dependency references missing node "ghost"`, func(d *Definition) {
			d.Dependencies = []Dependency{{From: "ghost", To: "a"}}
		}},
		{"dependency to missing node", `dependency references missing node "ghost"`, func(d *Definition) {
			d.Dependencies = []Dependency{{From: "a", To: "ghost"}}
		}},
		{"duplicate dependency", `duplicate dependency "a" -> "b"`, func(d *Definition) {
			d.Nodes = approvals("a", "b")
			d.Dependencies = []Dependency{{From: "a", To: "b"}, {From: "a", To: "b"}}
		}},
		{"invalid dependency condition", `dependency "a" -> "b":`, func(d *Definition) {
			d.Nodes = approvals("a", "b")
			d.Dependencies = []Dependency{{From: "a", To: "b", Condition: "!!!"}}
		}},
		{"cycle", "workflow contains a cycle", func(d *Definition) {
			d.Nodes = approvals("a", "b")
			d.Dependencies = []Dependency{{From: "a", To: "b"}, {From: "b", To: "a"}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDefinition(definitionWith(dir, tc.mutate))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateDefinition error = %v, want containing %q", err, tc.want)
			}
		})
	}
	if err := validateDefinition(definitionWith(dir, func(*Definition) {})); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	// A reusable definition with command nodes and no directory must
	// publish: the directory is resolved per run, not per definition.
	reusable := definitionWith("", func(d *Definition) {
		d.Nodes = []Node{{ID: "a", Name: "A", Type: "command", Command: []string{"true"}}}
	})
	if err := validateDefinition(reusable); err != nil {
		t.Fatalf("reusable definition without a directory rejected: %v", err)
	}
}

func TestValidateCommandNode(t *testing.T) {
	dir := t.TempDir()
	ok := Node{ID: "a", Name: "A", Type: "command", Command: []string{"true"}}
	tests := []struct {
		name      string
		directory string
		node      Node
		want      string
	}{
		{"no command", dir, Node{ID: "a", Name: "A", Type: "command"}, "command is required"},
		{"empty argv0", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{""}}, "command is required"},
		{"NUL in argument", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{"echo", "a\x00b"}}, "command contains NUL"},
		{"invalid env name", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{"true"},
			Environment: map[string]string{"1BAD": "x"}}, `invalid environment variable "1BAD"`},
		{"NUL in env value", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{"true"},
			Environment: map[string]string{"OK": "a\x00b"}}, `invalid environment variable "OK"`},
		{"non bash permission", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{"true"},
			Permission: []PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}}}, "requires bash and a pattern"},
		{"permission without pattern", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{"true"},
			Permission: []PermissionRule{{Permission: "bash", Action: "allow"}}}, "requires bash and a pattern"},
		{"invalid permission action", dir, Node{ID: "a", Name: "A", Type: "command", Command: []string{"true"},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "maybe"}}}, `invalid permission action "maybe"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCommandNode(tc.node)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateCommandNode error = %v, want containing %q", err, tc.want)
			}
		})
	}
	valid := ok
	valid.Environment = map[string]string{"OK_1": "value"}
	valid.Permission = []PermissionRule{{Permission: "bash", Pattern: "true*", Action: "allow"}}
	if err := validateCommandNode(valid); err != nil {
		t.Fatalf("valid command node rejected: %v", err)
	}
	// A reusable definition publishes fine without any directory; the
	// directory is a run-time concern.
	if err := validateCommandNode(ok); err != nil {
		t.Fatalf("command node rejected without a directory: %v", err)
	}
}

// TestCommandDirectoryCheckedAtRunTime proves a reusable definition without a
// directory publishes cleanly and only fails when a run actually needs a
// working directory.
func TestCommandDirectoryCheckedAtRunTime(t *testing.T) {
	h := newHarness(t)
	run := publishAndStart(t, h, Definition{
		ID: "reusable", Name: "Reusable", Version: "1", Concurrency: 1,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes: []Node{{ID: "a", Name: "A", Type: "command", Command: []string{"true"},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}},
	})
	done := waitForRun(t, h.svc, run.ID, StateFailed)
	if len(done.Nodes) != 1 || !strings.Contains(done.Nodes[0].Attempts[0].Error, "workflow directory must be absolute") {
		t.Fatalf("expected a run-time directory error, got %+v", done.Nodes)
	}
}

func TestValidateRunDirectory(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name      string
		directory string
		want      string
	}{
		{"empty directory", "", "workflow directory must be absolute"},
		{"relative directory", "relative/path", "workflow directory must be absolute"},
		{"missing directory", filepath.Join(dir, "nope"), "workflow directory must exist"},
		{"directory is a file", writeTempFile(t, dir), "workflow directory must exist"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRunDirectory(tc.directory)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateRunDirectory error = %v, want containing %q", err, tc.want)
			}
		})
	}
	if err := validateRunDirectory(dir); err != nil {
		t.Fatalf("valid directory rejected: %v", err)
	}
}

// writeTempFile creates a regular file so a "directory" check has
// something that exists but is not a directory.
func writeTempFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestValidateTrigger(t *testing.T) {
	tests := []struct {
		name    string
		trigger Trigger
		want    string
	}{
		{"missing id", Trigger{Type: TriggerManual}, "trigger id is required"},
		{"invalid overlap", Trigger{ID: "t", Type: TriggerManual, Overlap: "stack"}, `invalid overlap "stack"`},
		{"interval without seconds", Trigger{ID: "t", Type: TriggerInterval}, "intervalSeconds must be positive"},
		{"invalid cron", Trigger{ID: "t", Type: TriggerCron, Cron: "not a cron"}, "has invalid cron"},
		{"pr without number", Trigger{ID: "t", Type: TriggerPR, Directory: "/repo"}, "requires prNumber and directory"},
		{"pr without directory", Trigger{ID: "t", Type: TriggerPR, PRNumber: 7}, "requires prNumber and directory"},
		{"completion without session", Trigger{ID: "t", Type: TriggerChildCompletion}, "requires sessionId"},
		{"turn completion without session", Trigger{ID: "t", Type: TriggerTurnCompletion}, "requires sessionId"},
		{"unsupported type", Trigger{ID: "t", Type: "webhook"}, `unsupported type "webhook"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTrigger(tc.trigger)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateTrigger error = %v, want containing %q", err, tc.want)
			}
		})
	}
	valid := []Trigger{
		{ID: "m", Type: TriggerManual, Overlap: OverlapParallel},
		{ID: "i", Type: TriggerInterval, IntervalSeconds: 60, Overlap: OverlapQueue},
		{ID: "c", Type: TriggerCron, Cron: "*/5 * * * *"},
		{ID: "p", Type: TriggerPR, PRNumber: 1, Directory: "/repo"},
		{ID: "s", Type: TriggerTurnCompletion, SessionID: "ses_1"},
	}
	for _, trigger := range valid {
		if err := validateTrigger(trigger); err != nil {
			t.Fatalf("validateTrigger(%q) = %v, want nil", trigger.ID, err)
		}
	}
}

// TestNextCheckSchedulesEveryTriggerType pins the polling cadence, including
// the sub-minute interval clamp and the never-fired anchor.
func TestNextCheckSchedulesEveryTriggerType(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	lastFired := now.Add(-90 * time.Second).UnixMilli()
	tests := []struct {
		name      string
		trigger   Trigger
		lastFired int64
		want      int64
	}{
		{"interval clamped to one minute when never fired", Trigger{Type: TriggerInterval, IntervalSeconds: 5}, 0, now.Add(time.Minute).UnixMilli()},
		{"interval clamped to one minute after firing", Trigger{Type: TriggerInterval, IntervalSeconds: 5}, lastFired, time.UnixMilli(lastFired).Add(time.Minute).UnixMilli()},
		{"interval honours explicit period", Trigger{Type: TriggerInterval, IntervalSeconds: 600}, lastFired, time.UnixMilli(lastFired).Add(10 * time.Minute).UnixMilli()},
		{"cron uses the next slot", Trigger{Type: TriggerCron, Cron: "*/5 * * * *"}, 0, now.Add(5 * time.Minute).UnixMilli()},
		{"invalid cron has no next check", Trigger{Type: TriggerCron, Cron: "not a cron"}, 0, 0},
		{"pr polling clamped to thirty seconds", Trigger{Type: TriggerPR, PollSeconds: 1}, 0, now.Add(30 * time.Second).UnixMilli()},
		{"pr polling honours explicit period", Trigger{Type: TriggerPR, PollSeconds: 120}, 0, now.Add(2 * time.Minute).UnixMilli()},
		{"turn completion polls every five seconds", Trigger{Type: TriggerTurnCompletion}, 0, now.Add(5 * time.Second).UnixMilli()},
		{"manual is never polled", Trigger{Type: TriggerManual}, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextCheck(tc.trigger, tc.lastFired, now); got != tc.want {
				t.Fatalf("nextCheck = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestShouldFireRequiresConfiguredDependencies proves a trigger whose
// collaborator is missing reports an error rather than silently never
// firing, and that interval/cron decisions do not need a store.
func TestShouldFireRequiresConfiguredDependencies(t *testing.T) {
	// Deps{} exercises the nil-Now / nil-executor / nil-ResolveSecret
	// defaults too.
	bare := NewService(Deps{})
	if bare.now == nil || bare.executor == nil || bare.resolveSecret == nil {
		t.Fatal("NewService did not install its defaults")
	}
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	ctx := context.Background()
	tests := []struct {
		name    string
		trigger Trigger
		row     state.WorkflowTriggerState
		want    string
	}{
		{"child completion without status", Trigger{ID: "t", Type: TriggerChildCompletion, SessionID: "s"}, state.WorkflowTriggerState{}, "trigger requires session status"},
		{"turn completion without status", Trigger{ID: "t", Type: TriggerTurnCompletion, SessionID: "s"}, state.WorkflowTriggerState{}, "trigger requires session status"},
		{"pr without forge", Trigger{ID: "t", Type: TriggerPR, PRNumber: 1, Directory: "/repo"}, state.WorkflowTriggerState{}, "pr trigger requires a forge poller"},
		{"invalid cron", Trigger{ID: "t", Type: TriggerCron, Cron: "not a cron"}, state.WorkflowTriggerState{LastFiredAt: now.UnixMilli()}, "cron"},
		{"unknown type", Trigger{ID: "t", Type: "webhook"}, state.WorkflowTriggerState{}, `unknown workflow trigger "webhook"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fire, _, _, _, err := bare.shouldFire(ctx, tc.trigger, tc.row, now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("shouldFire error = %v, want containing %q", err, tc.want)
			}
			if fire {
				t.Fatal("shouldFire returned true alongside an error")
			}
		})
	}

	// A sub-minute interval is clamped, so a trigger that fired 5s ago is
	// not due even though its declared interval already elapsed.
	recent := state.WorkflowTriggerState{LastFiredAt: now.Add(-5 * time.Second).UnixMilli()}
	fire, detail, _, _, err := bare.shouldFire(ctx, Trigger{ID: "t", Type: TriggerInterval, IntervalSeconds: 1}, recent, now)
	if err != nil || fire || detail != "scheduled" {
		t.Fatalf("clamped interval fire=%v detail=%q err=%v", fire, detail, err)
	}
	stale := state.WorkflowTriggerState{LastFiredAt: now.Add(-2 * time.Minute).UnixMilli()}
	if fire, _, _, _, err = bare.shouldFire(ctx, Trigger{ID: "t", Type: TriggerInterval, IntervalSeconds: 1}, stale, now); err != nil || !fire {
		t.Fatalf("elapsed interval fire=%v err=%v", fire, err)
	}
}

func TestNodeLookupsReturnNilForUnknownNodes(t *testing.T) {
	nodes := []Node{{
		ID:     "a",
		Repeat: &RepeatConfig{Until: "true", MaxAttempts: 2},
		Lease:  &LeaseConfig{Mode: LeaseExclusive},
		Agent:  &AgentConfig{Directory: "/repo", Prompt: "go"},
	}}
	if repeatConfig(nodes, "a") == nil || nodeLease(nodes, "a") == nil || agentConfig(nodes, "a") == nil {
		t.Fatal("lookups missed the declared node")
	}
	if repeatConfig(nodes, "ghost") != nil {
		t.Error("repeatConfig found a config for an unknown node")
	}
	if nodeLease(nodes, "ghost") != nil {
		t.Error("nodeLease found a lease for an unknown node")
	}
	if agentConfig(nodes, "ghost") != nil {
		t.Error("agentConfig found a config for an unknown node")
	}
}

func TestBudgetExceededHonoursEveryLimit(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	run := func(limits *Limits, sessions ...string) RunDetail {
		detail := RunDetail{Run: Run{ID: "run_1", CreatedAt: now.Add(-time.Hour).UnixMilli()}}
		detail.Version.Definition.Limits = limits
		attempts := make([]Attempt, 0, len(sessions))
		for _, id := range sessions {
			attempts = append(attempts, Attempt{Platform: "opencode", SessionID: id})
		}
		detail.Nodes = []NodeRun{{NodeID: "a", Attempts: attempts}}
		return detail
	}
	tests := []struct {
		name   string
		usage  UsageSource
		detail RunDetail
		want   bool
		reason string
	}{
		{"no limits", nil, run(nil), false, ""},
		{"duration under limit", nil, run(&Limits{MaxDurationSecs: 7200}), false, ""},
		{"duration exceeded", nil, run(&Limits{MaxDurationSecs: 60}), true, "duration limit of 60s"},
		{"cost limit without usage source", nil, run(&Limits{MaxCostUSD: 1}, "s1"), false, ""},
		{"cost limit without sessions", &fakeWorkflowUsage{ok: true, perSessionCost: 10}, run(&Limits{MaxCostUSD: 1}), false, ""},
		{"usage unavailable", &fakeWorkflowUsage{ok: false}, run(&Limits{MaxCostUSD: 1}, "s1"), false, ""},
		{"cost exceeded", &fakeWorkflowUsage{ok: true, perSessionCost: 10}, run(&Limits{MaxCostUSD: 5}, "s1"), true, "cost limit of $5.00"},
		{"cost under limit", &fakeWorkflowUsage{ok: true, perSessionCost: 1}, run(&Limits{MaxCostUSD: 5}, "s1"), false, ""},
		{"tokens exceeded", &fakeWorkflowUsage{ok: true, perSessionTokens: 100}, run(&Limits{MaxTokens: 50}, "s1"), true, "token limit of 50"},
		{"tokens under limit", &fakeWorkflowUsage{ok: true, perSessionTokens: 10}, run(&Limits{MaxTokens: 50}, "s1"), false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(Deps{Usage: tc.usage, Now: func() time.Time { return now }})
			exceeded, reason := svc.budgetExceeded(context.Background(), tc.detail)
			if exceeded != tc.want || !strings.Contains(reason, tc.reason) {
				t.Fatalf("budgetExceeded = %v, %q; want %v containing %q", exceeded, reason, tc.want, tc.reason)
			}
		})
	}

	// A session reference is (platform, sessionID). Usage is asked for the
	// pair the attempt recorded, so a run is never billed for a same-id
	// session on another machine; an attempt that stored no platform is
	// left out rather than resolved by bare id.
	t.Run("usage is asked for the full identity", func(t *testing.T) {
		usage := &fakeWorkflowUsage{ok: true, perSessionCost: 1}
		svc := NewService(Deps{Usage: usage, Now: func() time.Time { return now }})
		detail := run(&Limits{MaxCostUSD: 100}, "s1")
		detail.Nodes[0].Attempts[0].Platform = "r-A:opencode"
		detail.Nodes[0].Attempts = append(detail.Nodes[0].Attempts, Attempt{SessionID: "s1"})
		svc.budgetExceeded(context.Background(), detail)
		asked := usage.askedFor()
		if len(asked) != 1 || asked[0] != (state.Key{Platform: "r-A:opencode", SessionID: "s1"}) {
			t.Fatalf("usage asked for %+v, want only the remote (platform, session) pair", asked)
		}
	})
}

func TestWorkspaceRequestAndGitRestriction(t *testing.T) {
	withLease := func(lease *LeaseConfig, workspace *WorkspaceConfig) Definition {
		return Definition{Workspace: workspace, Nodes: []Node{{ID: "a", Lease: lease}}}
	}
	shards := &WorkspaceConfig{Shards: 2, Repo: "/repo"}

	if workspaceRequest(withLease(&LeaseConfig{}, nil), "a") != nil {
		t.Error("workspaceRequest built a request without a shard pool")
	}
	if workspaceRequest(withLease(&LeaseConfig{}, &WorkspaceConfig{Shards: 0}), "a") != nil {
		t.Error("workspaceRequest built a request for an empty shard pool")
	}
	if workspaceRequest(withLease(nil, shards), "a") != nil {
		t.Error("workspaceRequest built a request for a node without a lease")
	}
	// An omitted mode defaults to exclusive.
	request := workspaceRequest(withLease(&LeaseConfig{}, shards), "a")
	if request == nil || request.Mode != LeaseExclusive || request.Shards != 2 || request.Host != "" {
		t.Fatalf("default lease request = %+v", request)
	}
	request = workspaceRequest(withLease(&LeaseConfig{Mode: LeasePath, Paths: []string{"pkg"}, Commit: false}, shards), "a")
	if request == nil || request.Mode != LeasePath || len(request.Paths) != 1 {
		t.Fatalf("path lease request = %+v", request)
	}

	if got := restrictGitFor(withLease(&LeaseConfig{Mode: LeasePath, Paths: []string{"pkg"}}, nil), "a"); got != nil {
		t.Errorf("restrictGitFor without a shard pool = %v", got)
	}
	if got := restrictGitFor(withLease(nil, shards), "a"); got != nil {
		t.Errorf("restrictGitFor without a lease = %v", got)
	}
	if got := restrictGitFor(withLease(&LeaseConfig{Mode: LeasePath, Commit: true}, shards), "a"); got != nil {
		t.Errorf("restrictGitFor for a commit coordinator = %v", got)
	}
	if got := restrictGitFor(withLease(&LeaseConfig{Mode: LeaseExclusive}, shards), "a"); got != nil {
		t.Errorf("restrictGitFor for an exclusive lease = %v", got)
	}
	if got := restrictGitFor(withLease(&LeaseConfig{Mode: LeasePath, Paths: []string{"pkg"}}, shards), "a"); len(got) != len(defaultRestrictedGitSubcommands) {
		t.Errorf("restrictGitFor default set = %v", got)
	}
	custom := &WorkspaceConfig{Shards: 2, RestrictedGit: []string{"push"}}
	if got := restrictGitFor(withLease(&LeaseConfig{Mode: LeasePath, Paths: []string{"pkg"}}, custom), "a"); len(got) != 1 || got[0] != "push" {
		t.Errorf("restrictGitFor custom set = %v", got)
	}
}

// retryFixture builds the source run / target version pair
// validateRetryBoundary compares, with "a" -> "b" and the retry point at "b".
func retryFixture(sourceNodes, targetNodes []Node, sourceDeps, targetDeps []Dependency, sourceState string) (RunDetail, Version, map[string]bool, map[string]bool) {
	source := RunDetail{}
	source.Version.Definition = Definition{Nodes: sourceNodes, Dependencies: sourceDeps}
	for _, node := range sourceNodes {
		run := NodeRun{NodeID: node.ID, Type: node.Type, State: sourceState,
			Attempts: []Attempt{{ID: 1, State: AttemptSuccessful}}}
		if sourceState != NodeSuccessful {
			run.Attempts = nil
		}
		source.Nodes = append(source.Nodes, run)
	}
	target := Version{Definition: Definition{Nodes: targetNodes, Dependencies: targetDeps}}
	sourceClosure, _ := descendantClosure(source.Version.Definition, "b")
	targetClosure, _ := descendantClosure(target.Definition, "b")
	return source, target, sourceClosure, targetClosure
}

func TestValidateRetryBoundaryRejectsChangedPrefixes(t *testing.T) {
	approval := func(id, name string) Node { return Node{ID: id, Name: name, Type: "approval"} }
	pair := []Node{approval("a", "A"), approval("b", "B")}
	edge := []Dependency{{From: "a", To: "b"}}

	tests := []struct {
		name        string
		source      []Node
		target      []Node
		sourceDeps  []Dependency
		targetDeps  []Dependency
		sourceState string
		want        string
	}{
		{"source map node", []Node{{ID: "a", Name: "A", Type: "map"}, approval("b", "B")}, pair, edge, edge, NodeSuccessful, "does not yet support map or join"},
		{"target join node", pair, []Node{{ID: "a", Name: "A", Type: "join"}, approval("b", "B")}, edge, edge, NodeSuccessful, "does not yet support map or join"},
		{"prefix node renamed", pair, []Node{approval("a", "Renamed"), approval("b", "B")}, edge, edge, NodeSuccessful, `node "a" before the retry point changed`},
		{"prefix node removed", pair, []Node{approval("b", "B")}, edge, nil, NodeSuccessful, `node "a" before the retry point changed`},
		{"prefix node did not succeed", pair, pair, edge, edge, NodeFailed, `node "a" before the retry point did not complete successfully`},
		{"prefix node added", []Node{approval("b", "B")}, pair, nil, edge, NodeSuccessful, `node "a" was added before the retry point`},
		{"dependencies changed", []Node{approval("a", "A"), approval("b", "B"), approval("c", "C")},
			[]Node{approval("a", "A"), approval("b", "B"), approval("c", "C")},
			[]Dependency{{From: "a", To: "c"}}, nil, NodeSuccessful, "dependencies before the retry point changed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source, target, sourceClosure, targetClosure := retryFixture(tc.source, tc.target, tc.sourceDeps, tc.targetDeps, tc.sourceState)
			err := validateRetryBoundary(source, target, sourceClosure, targetClosure)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateRetryBoundary error = %v, want containing %q", err, tc.want)
			}
		})
	}

	source, target, sourceClosure, targetClosure := retryFixture(pair, pair, edge, edge, NodeSuccessful)
	if err := validateRetryBoundary(source, target, sourceClosure, targetClosure); err != nil {
		t.Fatalf("identical versions rejected: %v", err)
	}
}

// TestRetryFromRejectsMismatchedTargets covers the version-resolution
// failures RetryFrom reports before it ever writes a derived run.
func TestRetryFromRejectsMismatchedTargets(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.Approve(ctx, run.ID, "review"); err != nil {
		t.Fatal(err)
	}
	if run, err = h.svc.Approve(ctx, run.ID, "ship"); err != nil {
		t.Fatal(err)
	}

	// An empty target resolves the workflow's current version.
	retried, err := h.svc.RetryFrom(ctx, run.ID, "ship", "")
	if err != nil {
		t.Fatalf("retry against the resolved active version: %v", err)
	}
	if retried.VersionID != version.ID || retried.RetryOfRunID != run.ID {
		t.Fatalf("retry pinned to %+v", retried.Run)
	}
	if _, err = h.svc.RetryFrom(ctx, run.ID, "ship", "wfv_missing"); err == nil {
		t.Fatal("retry against a missing version succeeded")
	}
	if _, err = h.svc.RetryFrom(ctx, run.ID, "ghost", version.ID); err == nil ||
		!strings.Contains(err.Error(), `node "ghost" must exist in both versions`) {
		t.Fatalf("retry from an unknown node error = %v", err)
	}
	if _, err = h.svc.RetryFrom(ctx, "wfr_missing", "ship", version.ID); err == nil {
		t.Fatal("retry of a missing run succeeded")
	}

	// A target pinned to a different workflow is refused.
	other := strings.Replace(sequentialApprovals, `"id":"release"`, `"id":"other"`, 1)
	otherVersion, err := h.svc.PublishJSON(ctx, []byte(other))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.RetryFrom(ctx, run.ID, "ship", otherVersion.ID); err == nil ||
		!strings.Contains(err.Error(), `retry version belongs to workflow "other"`) {
		t.Fatalf("cross-workflow retry error = %v", err)
	}

	// A version row whose stored definition is corrupt fails to decode.
	corrupt, err := h.db.InsertWorkflowVersion(state.WorkflowVersion{
		ID: "wfv_corrupt", WorkflowID: "release", Name: "Release",
		MetadataVersion: "corrupt", DefinitionJSON: "{not json", Concurrency: 1,
		CreatedAt: h.clock().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.RetryFrom(ctx, run.ID, "ship", corrupt.ID); err == nil ||
		!strings.Contains(err.Error(), "decoding stored workflow definition") {
		t.Fatalf("corrupt target retry error = %v", err)
	}
}

// TestCorruptStoredDefinitionSurfacesEverywhere proves every read path
// reports a corrupt persisted definition instead of returning a zero
// workflow.
func TestCorruptStoredDefinitionSurfacesEverywhere(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	row, err := h.db.InsertWorkflowVersion(state.WorkflowVersion{
		ID: "wfv_corrupt", WorkflowID: "broken", Name: "Broken",
		MetadataVersion: "1", DefinitionJSON: "{not json", Concurrency: 1,
		CreatedAt: h.clock().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = versionFromRow(row); err == nil || !strings.Contains(err.Error(), "decoding stored workflow definition") {
		t.Fatalf("versionFromRow error = %v", err)
	}
	calls := map[string]func() error{
		"GetVersion":       func() error { _, err := h.svc.GetVersion(ctx, row.ID); return err },
		"ListVersions":     func() error { _, err := h.svc.ListVersions(ctx); return err },
		"Start":            func() error { _, err := h.svc.Start(ctx, row.ID); return err },
		"GetTrigger":       func() error { _, err := h.svc.GetTrigger(ctx, row.ID, "manual"); return err },
		"EvaluateTriggers": func() error { return h.svc.EvaluateTriggers(ctx) },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "decoding stored workflow definition") {
				t.Fatalf("%s error = %v, want a decode failure", name, err)
			}
		})
	}
}

// TestServiceSurfacesStoreFailures closes the state DB under a live
// service so every read/write path has to report the failure instead of
// treating it as an empty result.
func TestServiceSurfacesStoreFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	svc := h.svc
	if err := h.db.Close(); err != nil {
		t.Fatalf("close state DB: %v", err)
	}
	h.db = nil

	calls := map[string]func() error{
		"PublishJSON":      func() error { _, err := svc.PublishJSON(ctx, []byte(sequentialApprovals)); return err },
		"Activate":         func() error { _, err := svc.Activate(ctx, "wfv_1"); return err },
		"Deactivate":       func() error { _, err := svc.Deactivate(ctx, "wfv_1"); return err },
		"Archive":          func() error { return svc.Archive(ctx, "wfv_1") },
		"GetVersion":       func() error { _, err := svc.GetVersion(ctx, "wfv_1"); return err },
		"ListVersions":     func() error { _, err := svc.ListVersions(ctx); return err },
		"ListRuns":         func() error { _, err := svc.ListRuns(ctx); return err },
		"GetRun":           func() error { _, err := svc.GetRun(ctx, "wfr_1"); return err },
		"GetTrigger":       func() error { _, err := svc.GetTrigger(ctx, "wfv_1", "manual"); return err },
		"Start":            func() error { _, err := svc.Start(ctx, "wfv_1"); return err },
		"StartActive":      func() error { _, err := svc.StartActive(ctx, "release"); return err },
		"RetryFrom":        func() error { _, err := svc.RetryFrom(ctx, "wfr_1", "ship", ""); return err },
		"Approve":          func() error { _, err := svc.Approve(ctx, "wfr_1", "review"); return err },
		"Pause":            func() error { _, err := svc.Pause(ctx, "wfr_1"); return err },
		"Resume":           func() error { _, err := svc.Resume(ctx, "wfr_1"); return err },
		"Cancel":           func() error { _, err := svc.Cancel(ctx, "wfr_1"); return err },
		"ResolveUnknown":   func() error { _, err := svc.ResolveUnknown(ctx, "wfr_1", 1, ResolutionSucceeded); return err },
		"EvaluateTriggers": func() error { return svc.EvaluateTriggers(ctx) },
		"triggerState":     func() error { _, err := svc.triggerState("wfv_1", "manual"); return err },
		"triggerStatuses": func() error {
			_, err := svc.triggerStatuses("wfv_1", []Trigger{{ID: "manual", Type: TriggerManual}})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatalf("%s succeeded against a closed store", name)
			}
		})
	}
}

// TestApplyPoliciesReportsStoreFailures drives the condition, repeat and
// fail-fast branches with a dead store so each write failure is returned.
func TestApplyPoliciesReportsStoreFailures(t *testing.T) {
	h := newHarness(t)
	svc := h.svc
	if err := h.db.Close(); err != nil {
		t.Fatalf("close state DB: %v", err)
	}
	h.db = nil
	ctx := context.Background()

	run := func(mutate func(*RunDetail)) RunDetail {
		detail := RunDetail{Run: Run{ID: "wfr_1", State: StateActive}}
		mutate(&detail)
		return detail
	}
	tests := []struct {
		name   string
		detail RunDetail
	}{
		{"fail fast", run(func(d *RunDetail) {
			d.Version.Definition = Definition{FailFast: true, Nodes: []Node{{ID: "a", Name: "A", Type: "command"}}}
			d.Nodes = []NodeRun{{NodeID: "a", Type: "command", State: NodeFailed}}
		})},
		{"false condition skips the node", run(func(d *RunDetail) {
			d.Version.Definition = Definition{
				Nodes:        []Node{{ID: "a", Name: "A", Type: "approval"}, {ID: "b", Name: "B", Type: "approval"}},
				Dependencies: []Dependency{{From: "a", To: "b", Condition: `outcomes["a"].state == "failed"`}},
			}
			d.Nodes = []NodeRun{{NodeID: "b", Type: "approval", State: NodeReady}}
		})},
		{"condition error skips the node", run(func(d *RunDetail) {
			d.Version.Definition = Definition{
				Nodes:        []Node{{ID: "a", Name: "A", Type: "approval"}, {ID: "b", Name: "B", Type: "approval"}},
				Dependencies: []Dependency{{From: "a", To: "b", Condition: `outcomes["ghost"].state == "failed"`}},
			}
			d.Nodes = []NodeRun{{NodeID: "b", Type: "approval", State: NodeReady}}
		})},
		{"repeat condition error exhausts", run(func(d *RunDetail) {
			d.Version.Definition = Definition{Nodes: []Node{{ID: "a", Name: "A", Type: "command",
				Repeat: &RepeatConfig{Until: `outcomes["ghost"].state == "ok"`, MaxAttempts: 3}}}}
			d.Nodes = []NodeRun{{NodeID: "a", Type: "command", State: NodeSuccessful,
				Attempts: []Attempt{{ID: 1, State: AttemptSuccessful}}}}
		})},
		{"repeat exhausted", run(func(d *RunDetail) {
			d.Version.Definition = Definition{Nodes: []Node{{ID: "a", Name: "A", Type: "command",
				Repeat: &RepeatConfig{Until: "false", MaxAttempts: 1}}}}
			d.Nodes = []NodeRun{{NodeID: "a", Type: "command", State: NodeSuccessful,
				Attempts: []Attempt{{ID: 1, State: AttemptSuccessful}}}}
		})},
		{"repeat schedules another attempt", run(func(d *RunDetail) {
			d.Version.Definition = Definition{Nodes: []Node{{ID: "a", Name: "A", Type: "command",
				Repeat: &RepeatConfig{Until: "false", MaxAttempts: 5}}}}
			d.Nodes = []NodeRun{{NodeID: "a", Type: "command", State: NodeSuccessful,
				Attempts: []Attempt{{ID: 1, State: AttemptSuccessful}}}}
		})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.applyPolicies(ctx, tc.detail); err == nil {
				t.Fatalf("applyPolicies succeeded against a closed store")
			}
		})
	}

	// A satisfied repeat predicate and a node with no repeat config are
	// both no-ops that never touch the store.
	quiet := run(func(d *RunDetail) {
		d.Version.Definition = Definition{Nodes: []Node{
			{ID: "a", Name: "A", Type: "command", Repeat: &RepeatConfig{Until: "true", MaxAttempts: 3}},
			{ID: "b", Name: "B", Type: "command"},
		}}
		d.Nodes = []NodeRun{
			{NodeID: "a", Type: "command", State: NodeSuccessful, Attempts: []Attempt{{ID: 1, State: AttemptSuccessful}}},
			{NodeID: "b", Type: "command", State: NodeSuccessful, Attempts: []Attempt{{ID: 2, State: AttemptSuccessful}}},
		}
	})
	moved, err := svc.applyPolicies(ctx, quiet)
	if moved || err != nil {
		t.Fatalf("applyPolicies(satisfied) = %v, %v; want false, nil", moved, err)
	}
}

// leaseFailingStore fails one run-detail read so GetRun's error handling is
// exercised without tearing down the whole store.
type leaseFailingStore struct {
	Store
	failResources bool
	failWorkspace bool
}

func (s leaseFailingStore) ListWorkflowResourceLeases(runID string) ([]state.WorkflowResourceLease, error) {
	if s.failResources {
		return nil, errors.New("resource lease read failed")
	}
	return s.Store.ListWorkflowResourceLeases(runID)
}

func (s leaseFailingStore) ListWorkflowWorkspaceLeases(runID string) ([]state.WorkflowWorkspaceLease, error) {
	if s.failWorkspace {
		return nil, errors.New("workspace lease read failed")
	}
	return s.Store.ListWorkflowWorkspaceLeases(runID)
}

// TestGetRunPropagatesLeaseReadFailures proves a run detail is never served
// with silently missing resource or workspace state.
func TestGetRunPropagatesLeaseReadFailures(t *testing.T) {
	tests := []struct {
		name  string
		store func(Store) Store
		want  string
	}{
		{"resource leases", func(inner Store) Store {
			return leaseFailingStore{Store: inner, failResources: true}
		}, "resource lease read failed"},
		{"workspace leases", func(inner Store) Store {
			return leaseFailingStore{Store: inner, failWorkspace: true}
		}, "workspace lease read failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := t.Context()
			version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.Start(ctx, version.ID)
			if err != nil {
				t.Fatal(err)
			}
			degraded := NewService(Deps{Store: tc.store(h.db), Now: h.clock})
			if _, err := degraded.GetRun(ctx, run.ID); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("GetRun error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// TestDispatchRefreshesRunAfterAPolicyMove proves a dispatch that skips a
// node re-reads the run before scheduling, so it never acts on the stale
// pre-skip snapshot. The store approval bypasses Approve's own policy pass
// so the skip decision is still pending when Tick dispatches.
func TestDispatchRefreshesRunAfterAPolicyMove(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	raw, err := json.Marshal(Definition{
		ID: "conditional", Name: "Conditional", Version: "1", Concurrency: 1,
		Triggers:     []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes:        []Node{{ID: "gate", Name: "Gate", Type: "approval"}, {ID: "branch", Name: "Branch", Type: "approval"}},
		Dependencies: []Dependency{{From: "gate", To: "branch", Condition: `outcomes["gate"].state == "failed"`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := h.svc.PublishJSON(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.db.ApproveWorkflowNode(run.ID, "gate", h.clock().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = h.svc.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	settled, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRun(t, settled, StateSuccessful, map[string]string{"gate": NodeSuccessful, "branch": NodeSkipped})
}

// TestApplyPoliciesFailFastStopsTheRun proves the fail-fast branch marks the
// run failed and reports that it moved the run.
func TestApplyPoliciesFailFastStopsTheRun(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The definition passed to applyPolicies drives the decision, so a
	// fail-fast graph with a failed node is enough to stop the live run.
	detail := RunDetail{Run: Run{ID: run.ID, State: StateActive}}
	detail.Version.Definition = Definition{FailFast: true,
		Nodes: []Node{{ID: "review", Name: "Review", Type: "approval"}}}
	detail.Nodes = []NodeRun{{NodeID: "review", Type: "approval", State: NodeFailed}}
	moved, err := h.svc.applyPolicies(ctx, detail)
	if err != nil || !moved {
		t.Fatalf("applyPolicies = %v, %v; want true, nil", moved, err)
	}
	failed, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed {
		t.Fatalf("run state = %q, want %q", failed.State, StateFailed)
	}
}

func TestInvalidMutationArgumentsAreRejected(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	version, err := h.svc.PublishJSON(ctx, []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.GetTrigger(ctx, version.ID, "ghost"); err == nil ||
		!strings.Contains(err.Error(), `workflow trigger "ghost" not found`) {
		t.Fatalf("GetTrigger error = %v", err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.ResolveUnknown(ctx, run.ID, 1, "maybe"); err == nil ||
		!strings.Contains(err.Error(), "resolution must be") {
		t.Fatalf("ResolveUnknown error = %v", err)
	}
	if _, err = h.svc.ResolveUnknown(ctx, run.ID, 999, ResolutionFailed); err == nil ||
		!strings.Contains(err.Error(), "resolving unknown attempt") {
		t.Fatalf("ResolveUnknown of an unknown attempt error = %v", err)
	}
	if _, err = h.svc.Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.Cancel(ctx, run.ID); err == nil ||
		!strings.Contains(err.Error(), "cannot be canceled from canceled") {
		t.Fatalf("second Cancel error = %v", err)
	}
	if _, err = h.svc.Start(ctx, version.ID); err != nil {
		t.Fatalf("restart after cancel: %v", err)
	}
}

// TestStartRequiresAManualTrigger proves an interval-only workflow cannot
// be started by hand.
func TestStartRequiresAManualTrigger(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	definition, err := json.Marshal(Definition{
		ID: "scheduled", Name: "Scheduled", Version: "1", Concurrency: 1,
		Triggers: []Trigger{{ID: "tick", Type: TriggerInterval, IntervalSeconds: 60}},
		Nodes:    []Node{{ID: "a", Name: "A", Type: "approval"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := h.svc.PublishJSON(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.Start(ctx, version.ID); err == nil ||
		!strings.Contains(err.Error(), "has no manual trigger") {
		t.Fatalf("Start error = %v", err)
	}
}

func TestFinalRunOutputCollapsesLeaves(t *testing.T) {
	leaf := func(id, nodeState, output string) NodeRun {
		node := NodeRun{NodeID: id, State: nodeState}
		if output != "" {
			node.Result.Output = json.RawMessage(output)
		}
		return node
	}
	tests := []struct {
		name string
		run  RunDetail
		want string
	}{
		{"no leaf output", RunDetail{Nodes: []NodeRun{leaf("a", NodeSkipped, "")}}, ""},
		{"single leaf passes through", RunDetail{Nodes: []NodeRun{leaf("a", NodeSuccessful, `{"ok":true}`)}}, `{"ok":true}`},
		{
			"multiple leaves are keyed by node",
			RunDetail{Nodes: []NodeRun{leaf("a", NodeSuccessful, `1`), leaf("b", NodeSuccessful, `2`)}},
			`{"a":1,"b":2}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := finalRunOutput(tc.run)
			if err != nil {
				t.Fatalf("finalRunOutput: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("finalRunOutput = %q, want %q", got, tc.want)
			}
		})
	}

	// A skipped downstream node does not hide its upstream's output.
	run := RunDetail{Nodes: []NodeRun{leaf("a", NodeSuccessful, `"kept"`), leaf("b", NodeSkipped, "")}}
	run.Version.Definition.Dependencies = []Dependency{{From: "a", To: "b"}}
	got, err := finalRunOutput(run)
	if err != nil || string(got) != `"kept"` {
		t.Fatalf("finalRunOutput with a skipped leaf = %q, %v", got, err)
	}
}

// TestResourceViewSeparatesHeldFromWaiting proves a node already holding a
// pool is not also reported as waiting on it.
func TestResourceViewSeparatesHeldFromWaiting(t *testing.T) {
	detail := RunDetail{Run: Run{ID: "wfr_1"}}
	detail.Version.Definition = Definition{
		Concurrency: 2,
		Pools:       []Pool{{Name: "build", Capacity: 1}},
		Nodes: []Node{
			{ID: "a", Name: "A", Type: "command", Resources: []ResourceRequest{{Pool: "build", Units: 1}}},
			{ID: "b", Name: "B", Type: "command", Resources: []ResourceRequest{{Pool: "build", Units: 1}}},
			{ID: "c", Name: "C", Type: "command"},
		},
	}
	detail.Nodes = []NodeRun{
		{NodeID: "a", Type: "command", State: NodeRunning, Attempts: []Attempt{{ID: 1, State: AttemptWaiting}}},
		{NodeID: "b", Type: "command", State: NodeReady, Attempts: []Attempt{{ID: 2, State: AttemptWaiting}}},
		{NodeID: "c", Type: "command", State: NodeSuccessful, Attempts: []Attempt{{ID: 3, State: AttemptSuccessful}}},
	}
	leases := []state.WorkflowResourceLease{
		{Pool: "", NodeID: "a", Units: 1},
		{Pool: "build", NodeID: "a", Units: 1},
	}
	pools := resourceView(detail, leases)
	if len(pools) != 2 {
		t.Fatalf("resourceView pools = %+v", pools)
	}
	if pools[0].Pool != "" || pools[0].Capacity != 2 || pools[0].Held != 1 {
		t.Fatalf("run pool = %+v", pools[0])
	}
	if pools[1].Pool != "build" || pools[1].Capacity != 1 || pools[1].Held != 1 {
		t.Fatalf("build pool = %+v", pools[1])
	}
	// "a" holds both pools, so only "b" waits; "c" is settled.
	for _, pool := range pools {
		if len(pool.Waiting) != 1 || pool.Waiting[0] != "b" {
			t.Fatalf("pool %q waiting = %v, want [b]", pool.Pool, pool.Waiting)
		}
	}
}
