package workflows

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type interpolationExecutor struct {
	mu       sync.Mutex
	requests []CommandRequest
}

func (e *interpolationExecutor) Execute(_ context.Context, request CommandRequest) CommandResult {
	e.mu.Lock()
	e.requests = append(e.requests, request)
	e.mu.Unlock()
	output := "null"
	if request.Command[0] == "produce" {
		output = `{"version":1,"large":9007199254740993,"literal":"${nodes.literal}","items":[{"id":7}]}`
	}
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: output}
}

func (e *interpolationExecutor) snapshot() []CommandRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]CommandRequest(nil), e.requests...)
}

func TestDependencyNodeResultsAreInterpolated(t *testing.T) {
	h := newHarness(t)
	executor := &interpolationExecutor{}
	h.svc = NewService(Deps{Store: h.db, Agent: h.agent, CommandExecutor: executor, Now: h.clock})
	const producerID = "sub/produce.v1"
	nodes := []Node{
		{ID: producerID, Name: "Produce", Type: "command", Command: []string{"produce"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		{ID: "sub/produce", Name: "Prefix", Type: "command", Command: []string{"prefix"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		{
			ID: "consume", Name: "Consume", Type: "command",
			Command: []string{
				"consume", `${nodes.sub/produce.v1}`, `${nodes.sub/produce.v1.id}`, `${nodes.sub/produce.v1.output}`,
				`${nodes.sub/produce.v1.output.items.0.id}`, `${nodes.sub/produce.v1.output.large}`, `${nodes.sub/produce.v1.output.literal}`,
			},
			Environment: map[string]string{"STATUS": `${nodes.sub/produce.v1.status}`, "VERSION": `${nodes.sub/produce.v1.output.version}`},
			Permission:  []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}},
		},
		{ID: "review", Name: "Review", Type: "agent", Agent: &AgentConfig{Platform: "test", Directory: "/repo", Prompt: `review ${nodes.sub/produce.v1.output.items}`}},
	}
	definition := commandDefinition(t, t.TempDir(), nodes, []Dependency{{From: producerID, To: "consume"}, {From: "sub/produce", To: "consume"}, {From: "consume", To: "review"}})
	version, err := h.svc.PublishJSON(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := h.svc.GetRun(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Nodes[3].State == NodeRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	requests := executor.snapshot()
	if len(requests) != 3 {
		t.Fatalf("command requests = %+v", requests)
	}
	var consumer CommandRequest
	for _, request := range requests {
		if request.Command[0] == "consume" {
			consumer = request
		}
	}
	wantCommand := []string{
		"consume",
		`{"ended":"2026-07-13T20:00:00Z","id":"sub/produce.v1","name":"Produce","output":{"items":[{"id":7}],"large":9007199254740993,"literal":"${nodes.literal}","version":1},"started":"2026-07-13T20:00:00Z","status":"successful"}`,
		`"sub/produce.v1"`,
		`{"items":[{"id":7}],"large":9007199254740993,"literal":"${nodes.literal}","version":1}`,
		"7", "9007199254740993", `"${nodes.literal}"`,
	}
	if fmt.Sprint(consumer.Command) != fmt.Sprint(wantCommand) || consumer.Environment["STATUS"] != `"successful"` || consumer.Environment["VERSION"] != "1" {
		t.Fatalf("interpolated command request = %+v", consumer)
	}
	if len(h.agent.starts) != 1 || h.agent.starts[0].Prompt != `review [{"id":7}]` {
		t.Fatalf("interpolated agent prompt = %+v", h.agent.starts)
	}
}

func TestInvalidNodeResultInterpolationFailsBeforeExecution(t *testing.T) {
	tests := []struct {
		name         string
		reference    string
		dependencies []Dependency
		environment  bool
	}{
		{name: "missing path", reference: `${nodes.produce.output.missing}`, dependencies: []Dependency{{From: "produce", To: "consume"}}},
		{name: "inaccessible node", reference: `${nodes.produce.output}`, dependencies: nil},
		{name: "malformed reference", reference: `${nodes.produce.output`, dependencies: []Dependency{{From: "produce", To: "consume"}}},
		{name: "trailing dot", reference: `${nodes.produce.}`, dependencies: []Dependency{{From: "produce", To: "consume"}}},
		{name: "invalid array index", reference: `${nodes.produce.output.items.nope}`, dependencies: []Dependency{{From: "produce", To: "consume"}}},
		{name: "missing array index", reference: `${nodes.produce.output.items.9}`, dependencies: []Dependency{{From: "produce", To: "consume"}}},
		{name: "scalar traversal", reference: `${nodes.produce.output.version.field}`, dependencies: []Dependency{{From: "produce", To: "consume"}}},
		{name: "environment reference", reference: `${nodes.produce.output.missing}`, dependencies: []Dependency{{From: "produce", To: "consume"}}, environment: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			executor := &interpolationExecutor{}
			h.svc = NewService(Deps{Store: h.db, CommandExecutor: executor, Now: h.clock})
			consumer := Node{ID: "consume", Name: "Consume", Type: "command", Command: []string{"consume"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}
			if test.environment {
				consumer.Environment = map[string]string{"VALUE": test.reference}
			} else {
				consumer.Command = append(consumer.Command, test.reference)
			}
			nodes := []Node{
				{ID: "produce", Name: "Produce", Type: "command", Command: []string{"produce"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
				consumer,
			}
			version, err := h.svc.PublishJSON(t.Context(), commandDefinition(t, t.TempDir(), nodes, test.dependencies))
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.Start(t.Context(), version.ID)
			if err != nil {
				t.Fatal(err)
			}
			failed := waitForRun(t, h.svc, run.ID, StateFailed)
			for _, request := range executor.snapshot() {
				if request.Command[0] == "consume" {
					t.Fatalf("consumer executed with invalid interpolation: %+v", request)
				}
			}
			if got := string(failed.Nodes[1].Result.Output); !strings.Contains(got, "interpolating workflow node results") {
				t.Fatalf("interpolation failure output = %s", got)
			}
		})
	}
}

func TestMappedItemCannotInjectNodeInterpolation(t *testing.T) {
	h := newHarness(t)
	agent := newScriptedAgent(nil)
	h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	run, _ := driveMap(t, h, version, items(`${nodes.seed.output}`))
	if run.State != StateSuccessful {
		t.Fatalf("mapped item node syntax was executed: %+v", run)
	}
}

func TestInvalidAgentInterpolationFailsBeforeSessionStart(t *testing.T) {
	h := newHarness(t)
	executor := &interpolationExecutor{}
	h.svc = NewService(Deps{Store: h.db, Agent: h.agent, CommandExecutor: executor, Now: h.clock})
	nodes := []Node{
		{ID: "produce", Name: "Produce", Type: "command", Command: []string{"produce"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		{ID: "review", Name: "Review", Type: "agent", Agent: &AgentConfig{Platform: "test", Directory: "/repo", Prompt: `${nodes.produce.output.missing}`}},
	}
	version, err := h.svc.PublishJSON(t.Context(), commandDefinition(t, t.TempDir(), nodes, []Dependency{{From: "produce", To: "review"}}))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForRun(t, h.svc, run.ID, StateFailed)
	if len(h.agent.starts) != 0 {
		t.Fatalf("agent started with invalid interpolation: %+v", h.agent.starts)
	}
	if got := string(failed.Nodes[1].Result.Output); !strings.Contains(got, "interpolating workflow node results") {
		t.Fatalf("agent interpolation failure output = %s", got)
	}
}
