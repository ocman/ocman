package workflows

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCollectorConfigurationIsRejected(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name       string
		definition string
		want       string
	}{
		{
			name:       "command outputs",
			definition: `{"id":"legacy","name":"Legacy","version":"1","concurrency":1,"directory":"/tmp","triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"run","name":"Run","type":"command","command":["true"],"outputs":[{"name":"result","type":"text"}]}]}`,
			want:       "command collectors are no longer supported",
		},
		{
			name:       "agent collectors",
			definition: `{"id":"legacy","name":"Legacy","version":"1","concurrency":1,"triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"run","name":"Run","type":"agent","agent":{"directory":"/tmp","prompt":"work","collectors":[{"name":"result","type":"final-message"}]}}]}`,
			want:       "agent collectors are no longer supported",
		},
		{name: "empty command outputs", definition: `{"id":"legacy","name":"Legacy","version":"1","concurrency":1,"directory":"/tmp","nodes":[{"id":"run","name":"Run","type":"command","command":["true"],"outputs":[]}]}`, want: "command collectors are no longer supported"},
		{name: "null command outputs", definition: `{"id":"legacy","name":"Legacy","version":"1","concurrency":1,"directory":"/tmp","nodes":[{"id":"run","name":"Run","type":"command","command":["true"],"outputs":null}]}`, want: "command collectors are no longer supported"},
		{name: "empty agent collectors", definition: `{"id":"legacy","name":"Legacy","version":"1","concurrency":1,"nodes":[{"id":"run","name":"Run","type":"agent","agent":{"directory":"/tmp","prompt":"work","collectors":[]}}]}`, want: "agent collectors are no longer supported"},
		{name: "null agent collectors", definition: `{"id":"legacy","name":"Legacy","version":"1","concurrency":1,"nodes":[{"id":"run","name":"Run","type":"agent","agent":{"directory":"/tmp","prompt":"work","collectors":null}}]}`, want: "agent collectors are no longer supported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := h.svc.ValidateJSON(t.Context(), []byte(test.definition))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

type resultMapExecutor struct{}

func (resultMapExecutor) Execute(_ context.Context, request CommandRequest) CommandResult {
	if request.Command[0] == "seed" {
		return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: `[{"id":"a"}]`}
	}
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: `{"ok":true}`}
}

func TestMapConsumesDependencyNodeResult(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, CommandExecutor: resultMapExecutor{}, Blobs: h.blobs, Now: func() time.Time { return h.now }})
	directory := t.TempDir()
	publishDefinition(t, h, Definition{
		ID: "item", Name: "Item", Version: "1", Concurrency: 1, Directory: directory,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes:    []Node{{ID: "work", Name: "Work", Type: "command", Command: []string{"work"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}},
	})
	version, err := h.svc.PublishJSON(t.Context(), commandDefinition(t, directory, []Node{
		{ID: "seed", Name: "Seed", Type: "command", Command: []string{"seed"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		{ID: "fan", Name: "Fan", Type: "map", Map: &MapConfig{Source: `${nodes.seed.output}`, Key: "id", Join: "join", Subworkflow: SubworkflowRef{WorkflowID: "item"}}},
		{ID: "join", Name: "Join", Type: "join", Join: &JoinConfig{Policy: JoinAllSuccess}},
	}, []Dependency{{From: "seed", To: "fan"}, {From: "fan", To: "join"}}))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && run.State == StateActive {
		time.Sleep(10 * time.Millisecond)
		if err := h.svc.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		run, err = h.svc.GetRun(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if run.State != StateSuccessful {
		t.Fatalf("run did not succeed: %+v", run)
	}
	if got := nodeOutput(t, run, "fan"); got != `{"items":[{"key":"a","index":0,"status":"successful","output":{"ok":true}}]}` {
		t.Fatalf("map output = %s", got)
	}
	artifacts, err := h.svc.ListArtifacts(t.Context(), run.Children[0].ChildRunID)
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("internal map item leaked as public artifact: %+v (%v)", artifacts, err)
	}
	internal, err := h.db.ListWorkflowArtifacts(run.Children[0].ChildRunID)
	if err != nil || len(internal) != 1 {
		t.Fatalf("internal map item missing: %+v (%v)", internal, err)
	}
	if _, _, err := h.svc.DownloadArtifact(t.Context(), run.Children[0].ChildRunID, internal[0].ID); err == nil {
		t.Fatal("internal map item was publicly downloadable")
	}
}

func TestMapRejectsLegacyArtifactSource(t *testing.T) {
	h := newHarness(t)
	directory := t.TempDir()
	publishDefinition(t, h, Definition{
		ID: "item", Name: "Item", Version: "1", Concurrency: 1, Directory: directory,
		Nodes: []Node{{ID: "work", Name: "Work", Type: "command", Command: []string{"work"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}},
	})
	for _, source := range []string{"items", `${nodes.seed}`, `${nodes.seed.status}`, `${nodes.unrelated.output}`} {
		_, err := h.svc.ValidateJSON(t.Context(), commandDefinition(t, directory, []Node{
			{ID: "seed", Name: "Seed", Type: "command", Command: []string{"seed"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "unrelated", Name: "Unrelated", Type: "command", Command: []string{"true"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "fan", Name: "Fan", Type: "map", Map: &MapConfig{Source: source, Key: "id", Join: "join", Subworkflow: SubworkflowRef{WorkflowID: "item"}}},
			{ID: "join", Name: "Join", Type: "join", Join: &JoinConfig{Policy: JoinAllSuccess}},
		}, []Dependency{{From: "seed", To: "fan"}, {From: "fan", To: "join"}}))
		if err == nil || !strings.Contains(err.Error(), "source must be one ${nodes.<dependency>.output} reference") {
			t.Fatalf("map source %q validation = %v", source, err)
		}
	}
}

func TestMapAndJoinValidationErrors(t *testing.T) {
	validMap := MapConfig{Source: `${nodes.seed.output}`, Key: "id", Join: "join", Subworkflow: SubworkflowRef{WorkflowID: "item"}}
	for _, test := range []struct {
		name string
		node Node
	}{
		{name: "missing map", node: Node{ID: "fan"}},
		{name: "missing key", node: Node{ID: "fan", Map: &MapConfig{Source: validMap.Source, Join: validMap.Join, Subworkflow: validMap.Subworkflow}}},
		{name: "missing subworkflow", node: Node{ID: "fan", Map: &MapConfig{Source: validMap.Source, Key: validMap.Key, Join: validMap.Join}}},
		{name: "missing join", node: Node{ID: "fan", Map: &MapConfig{Source: validMap.Source, Key: validMap.Key, Subworkflow: validMap.Subworkflow}}},
		{name: "unknown join", node: Node{ID: "fan", Map: &MapConfig{Source: validMap.Source, Key: validMap.Key, Join: "missing", Subworkflow: validMap.Subworkflow}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMapNode(test.node, map[string]bool{"join": true}, map[string]bool{"seed": true}); err == nil {
				t.Fatal("invalid map passed validation")
			}
		})
	}
	for _, node := range []Node{
		{ID: "join"},
		{ID: "join", Join: &JoinConfig{Policy: "unknown"}},
		{ID: "join", Join: &JoinConfig{Policy: JoinMinimumSuccess}},
	} {
		if err := validateJoinNode(node); err == nil {
			t.Fatal("invalid join passed validation")
		}
	}
}
