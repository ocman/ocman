package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// scriptedAgent drives per-item subworkflow agent nodes deterministically.
// Each launched session immediately reports the state configured for its
// item key (parsed from the prompt, which carries the item payload), so a
// single Tick settles the whole map. Out-of-order completion is modeled by
// the map/join reading persisted per-item state rather than launch order.
type scriptedAgent struct {
	mu       sync.Mutex
	counter  int
	outcome  map[string]string // item key -> "done" | "error"
	sessions map[string]string // session id -> item key
}

func newScriptedAgent(outcome map[string]string) *scriptedAgent {
	return &scriptedAgent{outcome: outcome, sessions: map[string]string{}}
}

func (a *scriptedAgent) Start(ctx context.Context, req AgentRequest) (AgentSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.counter++
	id := fmt.Sprintf("s%d", a.counter)
	// The item key is embedded in the prompt as "item:<key>".
	key := ""
	if i := strings.Index(req.Prompt, "item:"); i >= 0 {
		key = req.Prompt[i+len("item:"):]
	}
	a.sessions[id] = key
	return AgentSession{ID: id, Platform: req.Platform, State: "busy"}, nil
}

func (a *scriptedAgent) Inspect(ctx context.Context, session AgentSession) (AgentResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := a.sessions[session.ID]
	switch a.outcome[key] {
	case "error":
		return AgentResult{State: "error", Error: "item " + key + " failed"}, nil
	case "done", "":
		return AgentResult{State: "done", FinalMessage: `{"ok":true}`}, nil
	}
	return AgentResult{State: "done", FinalMessage: `{"ok":true}`}, nil
}

func (a *scriptedAgent) Cancel(context.Context, AgentSession) error { return nil }

// perItemAgent is a single-node subworkflow whose agent prompt carries the
// mapped item payload so the scripted agent can key its outcome.
const perItemAgent = `{
	"id":"item","name":"Item","version":"1","concurrency":4,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[{"id":"work","name":"Work","type":"agent","agent":{"platform":"test","directory":"/repo","prompt":"process item:${item.id}"}}]
}`

// mapWorkflow wires a producer (approval that publishes the array),
// a map node fanning across perItemAgent, and a join with the given policy.
func mapWorkflowJSON(policy string, minSuccess int, failFast bool) string {
	join := fmt.Sprintf(`{"id":"join","name":"Join","type":"join","join":{"policy":%q,"minSuccess":%d}}`, policy, minSuccess)
	return fmt.Sprintf(`{
		"id":"campaign","name":"Campaign","version":"1","concurrency":2,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[
			{"id":"seed","name":"Seed","type":"approval"},
			{"id":"fan","name":"Fan","type":"map","map":{"source":"${nodes.seed.output}","key":"id","join":"join","failFast":%t,"subworkflow":{"workflowId":"item"}}},
			%s,
			{"id":"report","name":"Report","type":"approval"}
		],
		"dependencies":[
			{"from":"seed","to":"fan"},
			{"from":"fan","to":"join"},
			{"from":"join","to":"report"}
		]
	}`, failFast, join)
}

func publishItemAndCampaign(t *testing.T, h *harness, policy string, minSuccess int, failFast bool) Version {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.PublishJSON(ctx, []byte(perItemAgent)); err != nil {
		t.Fatalf("publish item: %v", err)
	}
	version, err := h.svc.PublishJSON(ctx, []byte(mapWorkflowJSON(policy, minSuccess, failFast)))
	if err != nil {
		t.Fatalf("publish campaign: %v", err)
	}
	return version
}

func items(keys ...string) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]any{"id": key})
	}
	return out
}

func TestStableKeyValidation(t *testing.T) {
	for _, test := range []struct {
		item string
		want string
		err  bool
	}{
		{item: `{"id":"a"}`, want: "a"},
		{item: `{"id":2}`, want: "2"},
		{item: `[]`, err: true},
		{item: `{}`, err: true},
		{item: `{"id":false}`, err: true},
	} {
		got, err := stableKey(json.RawMessage(test.item), "id")
		if got != test.want || (err != nil) != test.err {
			t.Fatalf("stableKey(%s) = %q, %v", test.item, got, err)
		}
	}
}

// driveMap starts the campaign, approves seed, seeds the array, then ticks
// until the run settles or a bounded number of ticks elapse.
func driveMap(t *testing.T, h *harness, version Version, arr []map[string]any) (RunDetail, string) {
	t.Helper()
	ctx := context.Background()
	version = mapVersionWithItems(t, h, version, arr)
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := h.svc.GetRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == StateFailed || got.State == StateCanceled {
			return got, run.ID
		}
		// Join settled and report ready -> approve to finish.
		if nodeState(got, "report") == NodeReady {
			done, err := h.svc.Approve(ctx, run.ID, "report")
			if err != nil {
				t.Fatalf("approve report: %v", err)
			}
			return done, run.ID
		}
		h.advance()
		if err := h.svc.Tick(ctx); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	got, _ := h.svc.GetRun(ctx, run.ID)
	return got, run.ID
}

func mapVersionWithItems(t *testing.T, h *harness, version Version, arr []map[string]any) Version {
	t.Helper()
	payload, err := json.Marshal(arr)
	if err != nil {
		t.Fatal(err)
	}
	h.svc.executor = mapSeedExecutor{payload: string(payload), next: h.svc.executor}
	definition := version.Definition
	definition.Directory = t.TempDir()
	for index := range definition.Nodes {
		if definition.Nodes[index].ID == "seed" {
			definition.Nodes[index].Type = "command"
			definition.Nodes[index].Command = []string{"map-test-seed"}
			definition.Nodes[index].Permission = []PermissionRule{{Permission: "bash", Pattern: "map-test-seed", Action: "allow"}}
		}
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := h.svc.PublishJSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

type mapSeedExecutor struct {
	payload string
	next    CommandExecutor
}

func (e mapSeedExecutor) Execute(ctx context.Context, request CommandRequest) CommandResult {
	if request.Command[0] == "map-test-seed" {
		return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: e.payload}
	}
	return e.next.Execute(ctx, request)
}

func nodeState(run RunDetail, id string) string {
	for _, node := range run.Nodes {
		if node.NodeID == id {
			return node.State
		}
	}
	return ""
}

func TestMapEmptyListSkipsToJoin(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(nil), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	done, runID := driveMap(t, h, version, items())
	if done.State != StateSuccessful {
		t.Fatalf("empty map did not complete: %s (%s)", done.State, runID)
	}
	if nodeState(done, "join") != NodeSuccessful {
		t.Fatalf("empty join not successful: %s", nodeState(done, "join"))
	}
}

func TestMapRejectsDuplicateStableKeys(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(nil), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	done, _ := driveMap(t, h, version, items("a", "b", "a"))
	if done.State != StateFailed {
		t.Fatalf("duplicate keys did not fail the map: %s", done.State)
	}
	if !strings.Contains(mapNodeError(done, "fan"), "duplicate") {
		t.Fatalf("duplicate key error not reported: %q", mapNodeError(done, "fan"))
	}
}

func TestMapJoinInputOrderAndPerItemStatus(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(map[string]string{"b": "error"}), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAlways, 0, false)
	done, runID := driveMap(t, h, version, items("a", "b", "c"))
	if done.State != StateSuccessful {
		t.Fatalf("always join did not complete: %s", done.State)
	}
	result := joinResult(t, h, runID, "join")
	if len(result.Items) != 3 {
		t.Fatalf("join dropped items: %+v", result.Items)
	}
	wantOrder := []string{"a", "b", "c"}
	wantState := map[string]string{"a": "successful", "b": "failed", "c": "successful"}
	for i, item := range result.Items {
		if item.Key != wantOrder[i] {
			t.Fatalf("join out of input order at %d: %+v", i, result.Items)
		}
		if item.State != wantState[item.Key] {
			t.Fatalf("item %s state = %s, want %s", item.Key, item.State, wantState[item.Key])
		}
	}
}

func TestMapPartialFailureContinuesUnrelatedItems(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(map[string]string{"b": "error"}), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinMinimumSuccess, 2, false)
	done, runID := driveMap(t, h, version, items("a", "b", "c"))
	if done.State != StateSuccessful {
		t.Fatalf("minimum-success (2 of 3) should pass: %s", done.State)
	}
	result := joinResult(t, h, runID, "join")
	success := 0
	for _, item := range result.Items {
		if item.State == "successful" {
			success++
		}
	}
	if success != 2 {
		t.Fatalf("expected 2 successes despite one failure, got %d", success)
	}
}

func TestMapFailFastStopsRun(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(map[string]string{"b": "error"}), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAlways, 0, true)
	done, _ := driveMap(t, h, version, items("a", "b", "c"))
	if done.State != StateFailed {
		t.Fatalf("fail-fast did not stop the run: %s", done.State)
	}
}

func TestMapAllSuccessFailsWhenAnyItemFails(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(map[string]string{"c": "error"}), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	done, _ := driveMap(t, h, version, items("a", "b", "c"))
	if done.State != StateFailed {
		t.Fatalf("all-success join should fail when an item fails: %s", done.State)
	}
}

func TestMapRestartDoesNotReprocessCompletedKeys(t *testing.T) {
	h := newHarness(t)
	agent := newScriptedAgent(nil)
	h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	done, runID := driveMap(t, h, version, items("a", "b", "c"))
	if done.State != StateSuccessful {
		t.Fatalf("initial run did not complete: %s", done.State)
	}
	launched := agent.counter
	if launched != 3 {
		t.Fatalf("expected 3 item launches, got %d", launched)
	}
	// Restart and tick again: completed stable keys must not relaunch.
	h.restart()
	h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, Now: h.clock})
	if err := h.svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if agent.counter != launched {
		t.Fatalf("restart reprocessed completed keys: %d launches after restart", agent.counter-launched)
	}
	after, _ := h.svc.GetRun(context.Background(), runID)
	if after.State != StateSuccessful {
		t.Fatalf("run state changed after restart: %s", after.State)
	}
}

// TestMapResumesAfterRestartMidFlight restarts the service after the map
// has expanded and launched items but before they settle, then verifies
// the run resumes to success without dropping or duplicating any item.
func TestMapResumesAfterRestartMidFlight(t *testing.T) {
	h := newHarness(t)
	// pendingAgent leaves items "busy" until flipped, so a restart lands
	// with the map running and children in flight.
	pending := &pendingScriptedAgent{scriptedAgent: *newScriptedAgent(nil)}
	h.svc = NewService(Deps{Store: h.db, Agent: pending, Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	version = mapVersionWithItems(t, h, version, items("a", "b", "c"))
	ctx := context.Background()
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Expand + launch children while they stay busy.
	h.advance()
	if err := h.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	itemsMid, _ := h.db.ListWorkflowMapItems(t.Context(), run.ID, "fan")
	if len(itemsMid) != 3 {
		t.Fatalf("map did not expand all items before restart: %d", len(itemsMid))
	}
	// Restart, let items finish, and drive to completion.
	h.restart()
	pending.release()
	h.svc = NewService(Deps{Store: h.db, Agent: pending, Blobs: h.blobs, Now: h.clock})
	for i := 0; i < 50; i++ {
		got, _ := h.svc.GetRun(ctx, run.ID)
		if nodeState(got, "report") == NodeReady {
			if _, err := h.svc.Approve(ctx, run.ID, "report"); err != nil {
				t.Fatal(err)
			}
			break
		}
		if got.State == StateFailed {
			t.Fatalf("mid-flight restart failed the run")
		}
		h.advance()
		if err := h.svc.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	done, _ := h.svc.GetRun(ctx, run.ID)
	if done.State != StateSuccessful {
		t.Fatalf("run did not resume to success after mid-flight restart: %s", done.State)
	}
	after, _ := h.db.ListWorkflowMapItems(t.Context(), run.ID, "fan")
	if len(after) != 3 {
		t.Fatalf("restart changed item count: %d", len(after))
	}
}

// pendingScriptedAgent keeps every session busy until release() is called.
type pendingScriptedAgent struct {
	scriptedAgent
	released bool
}

func (a *pendingScriptedAgent) release() { a.mu.Lock(); a.released = true; a.mu.Unlock() }

func (a *pendingScriptedAgent) Inspect(ctx context.Context, session AgentSession) (AgentResult, error) {
	a.mu.Lock()
	released := a.released
	a.mu.Unlock()
	if !released {
		return AgentResult{State: "busy"}, nil
	}
	return a.scriptedAgent.Inspect(ctx, session)
}

func TestLargeSyntheticMapStaysBounded(t *testing.T) {
	h := newHarness(t)
	h.svc = NewService(Deps{Store: h.db, Agent: newScriptedAgent(nil), Blobs: h.blobs, Now: h.clock})
	version := publishItemAndCampaign(t, h, JoinAllSuccess, 0, false)
	var keys []string
	for i := 0; i < 200; i++ {
		keys = append(keys, fmt.Sprintf("k%d", i))
	}
	done, runID := driveMap(t, h, version, items(keys...))
	if done.State != StateSuccessful {
		t.Fatalf("large map did not complete: %s", done.State)
	}
	result := joinResult(t, h, runID, "join")
	if len(result.Items) != 200 {
		t.Fatalf("large map join lost items: %d", len(result.Items))
	}
	for i, item := range result.Items {
		if item.Key != fmt.Sprintf("k%d", i) {
			t.Fatalf("large map join lost input order at %d: %s", i, item.Key)
		}
	}
}

func mapNodeError(run RunDetail, mapNode string) string {
	for _, node := range run.Nodes {
		if node.NodeID == mapNode {
			for _, attempt := range node.Attempts {
				if attempt.Error != "" {
					return attempt.Error
				}
			}
		}
	}
	return ""
}

func joinResult(t *testing.T, h *harness, runID, joinNode string) JoinResult {
	t.Helper()
	run, err := h.svc.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range run.Nodes {
		if node.NodeID != joinNode {
			continue
		}
		var canonical struct {
			Policy  string `json:"policy"`
			Success int    `json:"success"`
			Failed  int    `json:"failed"`
			Total   int    `json:"total"`
			Items   []struct {
				Key    string `json:"key"`
				Index  int    `json:"index"`
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(node.Result.Output, &canonical); err != nil {
			t.Fatalf("decoding join result: %v", err)
		}
		result := JoinResult{Policy: canonical.Policy, Success: canonical.Success, Failed: canonical.Failed, Total: canonical.Total}
		for _, item := range canonical.Items {
			result.Items = append(result.Items, JoinItem{Key: item.Key, Index: item.Index, State: item.Status})
		}
		return result
	}
	t.Fatalf("join node %q produced no result", joinNode)
	return JoinResult{}
}
