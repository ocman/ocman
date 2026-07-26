package server

import (
	"encoding/json"
	"testing"
)

func TestInterpolateStepPromptResolvesUpstreamAndItem(t *testing.T) {
	prompt := interpolateStepPrompt(
		"fix ${item.path} using tag ${nodes.build.output.tag} and all ${nodes.build.output}",
		json.RawMessage(`{"build":{"tag":"v1.2.3"}}`),
		json.RawMessage(`{"path":"src/parser.ts"}`),
	)
	want := `fix src/parser.ts using tag v1.2.3 and all {"tag":"v1.2.3"}`
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

// A prompt with nothing to resolve must survive untouched, and a
// reference with no matching payload must not corrupt the rest.
func TestInterpolateStepPromptToleratesMissingPayloads(t *testing.T) {
	if got := interpolateStepPrompt("plain prompt", nil, nil); got != "plain prompt" {
		t.Fatalf("prompt = %q", got)
	}
	if got := interpolateStepPrompt("x ${item.missing} y", nil, json.RawMessage(`{"a":1}`)); got != "x  y" {
		t.Fatalf("prompt = %q", got)
	}
}

// Downstream references resolve through the runner's JSON path support,
// so a JSON answer must pass through rather than be double-encoded.
func TestAgentStepOutputPreservesJSONAndWrapsProse(t *testing.T) {
	if got := string(agentStepOutput(`{"status":"done"}`)); got != `{"status":"done"}` {
		t.Errorf("json output = %s", got)
	}
	if got := string(agentStepOutput("```json\n{\"a\":1}\n```")); got != `{"a":1}` {
		t.Errorf("fenced output = %s", got)
	}
	if got := string(agentStepOutput("all good")); got != `"all good"` {
		t.Errorf("prose output = %s", got)
	}
}

func TestWorkflowStepOutcomesShapesConditionEnvironment(t *testing.T) {
	outcomes := workflowStepOutcomes(json.RawMessage(`{"build":{"ok":true}}`))
	build, ok := outcomes["build"].(map[string]any)
	if !ok {
		t.Fatalf("outcomes = %#v", outcomes)
	}
	output, ok := build["output"].(map[string]any)
	if !ok || output["ok"] != true {
		t.Fatalf("build = %#v", build)
	}
	if got := workflowStepOutcomes(nil); len(got) != 0 {
		t.Errorf("empty upstream = %#v", got)
	}
}

// A wildcard or empty bind address still has to yield a URL the shim can
// reach, and the callback must stay on loopback.
func TestLoopbackEndpointKeepsCallbackLocal(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8229": "http://127.0.0.1:8229",
		"0.0.0.0:9000":   "http://127.0.0.1:9000",
		":7000":          "http://127.0.0.1:7000",
		"garbage":        "http://127.0.0.1:8229",
		"":               "http://127.0.0.1:8229",
	} {
		if got := loopbackEndpoint(addr); got != want {
			t.Errorf("loopbackEndpoint(%q) = %q, want %q", addr, got, want)
		}
	}
}

// The runner spawns `ocman workflow-step`, which needs ocman's real
// address to call back on. Defining the setter without calling it left
// the shim falling back to the default port, which only works when
// ocman happens to be bound there.
func TestLocalHostWiresTheShimCallbackEndpoint(t *testing.T) {
	s := testServer(t)
	s.addr = "127.0.0.1:9411"
	s.daguManager = nil
	s.newLocalHost()
	if s.daguManager == nil {
		t.Fatal("local host did not build a Dagu manager")
	}
	if got := s.daguManager.OcmanEndpoint(); got != "http://127.0.0.1:9411" {
		t.Fatalf("shim callback endpoint = %q, want http://127.0.0.1:9411", got)
	}
}
