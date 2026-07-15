package workflows

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestApprovalPublishesNodeResult(t *testing.T) {
	h := newHarness(t)
	version, err := h.svc.PublishJSON(t.Context(), []byte(sequentialApprovals))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.advance()
	run, err = h.svc.Approve(t.Context(), run.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(run.Nodes[0].Result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"review","name":"Review","started":"2026-07-13T20:00:00Z","ended":"2026-07-13T20:01:00Z","status":"successful","output":{"approved":true,"approvedBy":null,"approvedAt":"2026-07-13T20:01:00Z"}}`
	if string(got) != want {
		t.Fatalf("approval result:\n got %s\nwant %s", got, want)
	}
}

func TestNodeResultReadsLegacyJoinEnvelope(t *testing.T) {
	node := NodeRun{NodeID: "join", Name: "Join", Type: "join", State: NodeSuccessful, Attempts: []Attempt{{State: AttemptSuccessful, outputsJSON: `{"result":{"policy":"always","items":[]}}`}}}
	if got := string(nodeResult(node).Output); got != `{"policy":"always","items":[]}` {
		t.Fatalf("legacy join output = %s", got)
	}
}

func TestMapAndJoinPublishChildOutputs(t *testing.T) {
	h := newHarness(t)
	agent := newScriptedAgent(map[string]string{"a": "done", "b": "error"})
	h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, Now: func() time.Time { return h.now }})
	version := publishItemAndCampaign(t, h, JoinAlways, 0, false)
	run, _ := driveMap(t, h, version, items("a", "b"))
	if run.State != StateSuccessful {
		t.Fatalf("run state = %s", run.State)
	}
	mapOutput := nodeOutput(t, run, "fan")
	wantMap := `{"items":[{"key":"a","index":0,"status":"successful","output":{"ok":true}},{"key":"b","index":1,"status":"failed","output":{"error":"item b failed"}}]}`
	if mapOutput != wantMap {
		t.Fatalf("map output:\n got %s\nwant %s", mapOutput, wantMap)
	}
	joinOutput := nodeOutput(t, run, "join")
	wantJoin := `{"policy":"always","success":1,"failed":1,"total":2,"items":[{"key":"a","index":0,"status":"successful","output":{"ok":true}},{"key":"b","index":1,"status":"failed","output":{"error":"item b failed"}}]}`
	if joinOutput != wantJoin {
		t.Fatalf("join output:\n got %s\nwant %s", joinOutput, wantJoin)
	}
	stored, err := h.db.GetWorkflowRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range stored.Nodes {
		if node.NodeID == "fan" && node.Attempts[0].OutputsJSON != wantMap {
			t.Fatalf("stored map output = %s", node.Attempts[0].OutputsJSON)
		}
		if node.NodeID == "join" && node.Attempts[0].OutputsJSON != wantJoin {
			t.Fatalf("stored join output = %s", node.Attempts[0].OutputsJSON)
		}
	}
	h.restart()
	restarted, err := h.svc.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeOutput(t, restarted, "fan"); got != wantMap {
		t.Fatalf("restarted map output = %s", got)
	}
	if got := nodeOutput(t, restarted, "join"); got != wantJoin {
		t.Fatalf("restarted join output = %s", got)
	}
}

func TestFailedMapAndJoinKeepDiagnostics(t *testing.T) {
	t.Run("join policy failure", func(t *testing.T) {
		h := newHarness(t)
		agent := newScriptedAgent(map[string]string{"a": "done", "b": "error"})
		h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, Now: func() time.Time { return h.now }})
		version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
		run, _ := driveMap(t, h, version, items("a", "b"))
		if run.State != StateFailed {
			t.Fatalf("run state = %s", run.State)
		}
		got := nodeOutput(t, run, "join")
		want := `{"policy":"all-success","success":1,"failed":1,"total":2,"items":[{"key":"a","index":0,"status":"successful","output":{"ok":true}},{"key":"b","index":1,"status":"failed","output":{"error":"item b failed"}}],"error":"join policy \"all-success\" not satisfied (1/2 succeeded)"}`
		if got != want {
			t.Fatalf("failed join output:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("map fail-fast", func(t *testing.T) {
		h := newHarness(t)
		agent := newScriptedAgent(map[string]string{"a": "error"})
		h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, Now: func() time.Time { return h.now }})
		version := publishItemAndCampaign(t, h, JoinAlways, 0, true)
		run, _ := driveMap(t, h, version, items("a"))
		if run.State != StateFailed {
			t.Fatalf("run state = %s", run.State)
		}
		got := nodeOutput(t, run, "fan")
		want := `{"items":[{"key":"a","index":0,"status":"failed","output":{"error":"item a failed"}}],"error":"map stopped by fail-fast after an item failed"}`
		if got != want {
			t.Fatalf("failed map output:\n got %s\nwant %s", got, want)
		}
	})
}

func TestEmptyMapPublishesEmptyItems(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(nil), Blobs: h.blobs, Now: func() time.Time { return h.now }})
	version := publishItemAndCampaign(t, h, JoinAlways, 0, false)
	run, _ := driveMap(t, h, version, items())
	if got := nodeOutput(t, run, "fan"); got != `{"items":[]}` {
		t.Fatalf("empty map output = %s", got)
	}
}

type childCommandExecutor struct{ fail bool }

func (e childCommandExecutor) Execute(_ context.Context, request CommandRequest) CommandResult {
	if e.fail && request.Command[0] == "first" {
		return CommandResult{State: AttemptFailed, ExitCode: 1, Error: "child failed"}
	}
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: `{"node":"` + request.Command[0] + `"}`}
}

type nestedMapExecutor struct{}

func (nestedMapExecutor) Execute(_ context.Context, request CommandRequest) CommandResult {
	if request.Command[0] == "nested-seed" {
		return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: `[{"id":"nested"}]`}
	}
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: `{"leaf":true}`}
}

func TestMapChildOutputUsesEffectiveWorkflowLeaves(t *testing.T) {
	t.Run("parallel leaves", func(t *testing.T) {
		h := newHarness(t)
		h.svc = NewService(Deps{Store: h.db, CommandExecutor: childCommandExecutor{}, Blobs: h.blobs, Now: func() time.Time { return h.now }})
		publishCommandChild(t, h, nil)
		version, err := h.svc.PublishJSON(t.Context(), []byte(mapWorkflowJSON(JoinAlways, 0, false)))
		if err != nil {
			t.Fatal(err)
		}
		run, _ := driveMap(t, h, version, items("a"))
		want := `{"items":[{"key":"a","index":0,"status":"successful","output":{"first":{"node":"first"},"second":{"node":"second"}}}]}`
		if got := nodeOutput(t, run, "fan"); got != want {
			t.Fatalf("parallel child output:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("failed node before skipped leaf", func(t *testing.T) {
		h := newHarness(t)
		h.svc = NewService(Deps{Store: h.db, CommandExecutor: childCommandExecutor{fail: true}, Blobs: h.blobs, Now: func() time.Time { return h.now }})
		publishCommandChild(t, h, []Dependency{{From: "first", To: "second"}})
		version, err := h.svc.PublishJSON(t.Context(), []byte(mapWorkflowJSON(JoinAlways, 0, false)))
		if err != nil {
			t.Fatal(err)
		}
		run, _ := driveMap(t, h, version, items("a"))
		want := `{"items":[{"key":"a","index":0,"status":"failed","output":{"error":"child failed"}}]}`
		if got := nodeOutput(t, run, "fan"); got != want {
			t.Fatalf("failed child output:\n got %s\nwant %s", got, want)
		}
	})
}

func TestSkippedMapAndJoinKeepOutputSchemas(t *testing.T) {
	h := newHarness(t)
	version := publishItemAndCampaign(t, h, JoinAlways, 0, false)
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err = h.svc.Cancel(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeOutput(t, run, "fan"); got != `{"items":[]}` {
		t.Fatalf("skipped map output = %s", got)
	}
	if got := nodeOutput(t, run, "join"); got != `{"policy":"always","success":0,"failed":0,"total":0,"items":[]}` {
		t.Fatalf("skipped join output = %s", got)
	}
}

func TestNestedMapOutputLoadsDescendantResult(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, CommandExecutor: nestedMapExecutor{}, Blobs: h.blobs, Now: func() time.Time { return h.now }})
	directory := t.TempDir()
	leaf := Definition{
		ID: "leaf", Name: "Leaf", Version: "1", Concurrency: 1, Directory: directory,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes:    []Node{{ID: "leaf", Name: "Leaf", Type: "command", Command: []string{"leaf"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}},
	}
	publishDefinition(t, h, leaf)
	nested := Definition{
		ID: "item", Name: "Nested item", Version: "1", Concurrency: 2, Directory: directory,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes: []Node{
			{ID: "seed", Name: "Seed", Type: "command", Command: []string{"nested-seed"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "fan", Name: "Fan", Type: "map", Map: &MapConfig{Source: `${nodes.seed.output}`, Key: "id", Join: "join", Subworkflow: SubworkflowRef{WorkflowID: "leaf"}}},
			{ID: "join", Name: "Join", Type: "join", Join: &JoinConfig{Policy: JoinAllSuccess}},
		},
		Dependencies: []Dependency{{From: "seed", To: "fan"}, {From: "fan", To: "join"}},
	}
	publishDefinition(t, h, nested)
	version, err := h.svc.PublishJSON(t.Context(), []byte(mapWorkflowJSON(JoinAlways, 0, false)))
	if err != nil {
		t.Fatal(err)
	}
	run, _ := driveMap(t, h, version, items("outer"))
	want := `{"items":[{"key":"outer","index":0,"status":"successful","output":{"policy":"all-success","success":1,"failed":0,"total":1,"items":[{"key":"nested","index":0,"status":"successful","output":{"leaf":true}}]}}]}`
	if got := nodeOutput(t, run, "fan"); got != want {
		t.Fatalf("nested map output:\n got %s\nwant %s", got, want)
	}
}

func TestCanceledMapAndJoinCarryCanceledChild(t *testing.T) {
	h := newHarness(t)
	version := publishItemAndCampaign(t, h, JoinAlways, 0, false)
	version = mapVersionWithItems(t, h, version, items("a"))
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	for len(run.Children) == 0 {
		if err := h.svc.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		run, err = h.svc.GetRun(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err = h.svc.Cancel(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantMap := `{"items":[{"key":"a","index":0,"status":"canceled","output":null}]}`
	if got := nodeOutput(t, run, "fan"); got != wantMap {
		t.Fatalf("canceled map output:\n got %s\nwant %s", got, wantMap)
	}
	wantJoin := `{"policy":"always","success":0,"failed":1,"total":1,"items":[{"key":"a","index":0,"status":"canceled","output":null}]}`
	if got := nodeOutput(t, run, "join"); got != wantJoin {
		t.Fatalf("canceled join output:\n got %s\nwant %s", got, wantJoin)
	}
}

func publishCommandChild(t *testing.T, h *harness, dependencies []Dependency) {
	t.Helper()
	definition := Definition{
		ID: "item", Name: "Item", Version: "1", Concurrency: 2, Directory: t.TempDir(),
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes: []Node{
			{ID: "first", Name: "First", Type: "command", Command: []string{"first"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
			{ID: "second", Name: "Second", Type: "command", Command: []string{"second"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
		},
		Dependencies: dependencies,
	}
	publishDefinition(t, h, definition)
}

func publishDefinition(t *testing.T, h *harness, definition Definition) {
	t.Helper()
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PublishJSON(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
}

func nodeOutput(t *testing.T, run RunDetail, nodeID string) string {
	t.Helper()
	for _, node := range run.Nodes {
		if node.NodeID == nodeID {
			return string(node.Result.Output)
		}
	}
	t.Fatalf("node %q not found", nodeID)
	return ""
}
