package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

// The external runner executes agent, approval, and conditional nodes by
// invoking `ocman workflow-step`, which posts here. Ocman keeps the real
// node configuration, so the runner never sees a prompt, a model, or a
// credential — only that a step succeeded or failed.

// workflowStepPollInterval bounds how often a blocking step re-reads its
// state. Approvals are human-scale and agent turns are minute-scale, so
// a slow poll is ample and keeps an idle wait almost free.
var workflowStepPollInterval = 2 * time.Second

type workflowStepRequest struct {
	Kind      string          `json:"kind"`
	RunID     string          `json:"runId"`
	NodeID    string          `json:"nodeId"`
	From      string          `json:"from"`
	ParentRun string          `json:"parentRunId"`
	MapNode   string          `json:"mapNodeId"`
	ItemKey   string          `json:"itemKey"`
	Item      json.RawMessage `json:"item"`
	Upstream  json.RawMessage `json:"upstream"`
}

type workflowStepResponse struct {
	OK     bool            `json:"ok"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (s *Server) handleWorkflowStep(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "workflows are unavailable", http.StatusServiceUnavailable)
		return
	}
	var body workflowStepRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.NodeID == "" {
		http.Error(w, "nodeId is required", http.StatusBadRequest)
		return
	}
	runID := body.RunID
	if runID == "" {
		runID = body.ParentRun
	}
	if runID == "" {
		http.Error(w, "runId is required", http.StatusBadRequest)
		return
	}
	result, err := s.executeWorkflowStep(r.Context(), runID, body)
	if err != nil {
		writeJSON(w, workflowStepResponse{Error: err.Error()})
		return
	}
	writeJSON(w, result)
}

func (s *Server) executeWorkflowStep(ctx context.Context, runID string, body workflowStepRequest) (workflowStepResponse, error) {
	node, err := s.workflowStepNode(runID, body.NodeID)
	if err != nil {
		return workflowStepResponse{}, err
	}
	switch body.Kind {
	case "condition":
		return s.evaluateWorkflowCondition(runID, body)
	case "approval":
		return s.awaitWorkflowApproval(ctx, runID, body.NodeID)
	case "agent":
		return s.runWorkflowAgentStep(ctx, node, body)
	default:
		return workflowStepResponse{}, fmt.Errorf("unsupported workflow step kind %q", body.Kind)
	}
}

// workflowStepNode resolves the authored node behind a step. The runner
// only ever sends ids, so this is where a step is bound back to the
// immutable version its run pinned.
func (s *Server) workflowStepNode(runID, nodeID string) (workflows.Node, error) {
	run, err := s.stateDB.GetWorkflowRun(runID)
	if err != nil {
		return workflows.Node{}, fmt.Errorf("unknown run %q", runID)
	}
	version, err := s.stateDB.GetWorkflowVersion(run.VersionID)
	if err != nil || version == nil {
		return workflows.Node{}, fmt.Errorf("unknown version for run %q", runID)
	}
	var definition workflows.Definition
	if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
		return workflows.Node{}, fmt.Errorf("reading definition: %w", err)
	}
	for _, node := range definition.Nodes {
		if node.ID == nodeID {
			return node, nil
		}
	}
	return workflows.Node{}, fmt.Errorf("node %q is not part of run %q", nodeID, runID)
}

// evaluateWorkflowCondition runs an edge condition through ocman's CEL
// sandbox. A false condition is not an error: the runner turns a
// non-zero exit into a skip, which is the same semantics the native
// dispatcher applies.
func (s *Server) evaluateWorkflowCondition(runID string, body workflowStepRequest) (workflowStepResponse, error) {
	run, err := s.stateDB.GetWorkflowRun(runID)
	if err != nil {
		return workflowStepResponse{}, fmt.Errorf("unknown run %q", runID)
	}
	version, err := s.stateDB.GetWorkflowVersion(run.VersionID)
	if err != nil || version == nil {
		return workflowStepResponse{}, fmt.Errorf("unknown version for run %q", runID)
	}
	var definition workflows.Definition
	if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
		return workflowStepResponse{}, err
	}
	expression := ""
	for _, dependency := range definition.Dependencies {
		if dependency.To == body.NodeID && dependency.From == body.From {
			expression = dependency.Condition
		}
	}
	if expression == "" {
		// No condition means the edge is unconditional; let it through
		// rather than skipping a step the author never gated.
		return workflowStepResponse{OK: true}, nil
	}
	ok, err := workflows.EvaluateCondition(expression, workflowStepOutcomes(body.Upstream))
	if err != nil {
		return workflowStepResponse{}, err
	}
	return workflowStepResponse{OK: ok}, nil
}

// workflowStepOutcomes shapes the runner's upstream payload into the
// `outcomes` map a condition is allowed to see.
func workflowStepOutcomes(upstream json.RawMessage) map[string]any {
	outcomes := map[string]any{}
	if len(upstream) == 0 {
		return outcomes
	}
	var decoded map[string]any
	if err := json.Unmarshal(upstream, &decoded); err != nil {
		return outcomes
	}
	for id, output := range decoded {
		outcomes[id] = map[string]any{"output": output}
	}
	return outcomes
}

// awaitWorkflowApproval blocks until a human settles the node through
// the existing approve endpoint, so the run view's approve button keeps
// working untouched.
func (s *Server) awaitWorkflowApproval(ctx context.Context, runID, nodeID string) (workflowStepResponse, error) {
	ticker := time.NewTicker(workflowStepPollInterval)
	defer ticker.Stop()
	for {
		run, err := s.stateDB.GetWorkflowRun(runID)
		if err != nil {
			return workflowStepResponse{}, err
		}
		for _, node := range run.Nodes {
			if node.NodeID != nodeID {
				continue
			}
			switch node.State {
			case workflows.NodeSuccessful:
				return workflowStepResponse{OK: true}, nil
			case workflows.NodeFailed, workflows.NodeCanceled, workflows.NodeSkipped:
				return workflowStepResponse{Error: "approval was not granted"}, nil
			}
		}
		select {
		case <-ctx.Done():
			return workflowStepResponse{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// runWorkflowAgentStep drives one agent node to completion through the
// same executor the native dispatcher uses.
func (s *Server) runWorkflowAgentStep(ctx context.Context, node workflows.Node, body workflowStepRequest) (workflowStepResponse, error) {
	if node.Agent == nil {
		return workflowStepResponse{}, fmt.Errorf("node %q has no agent configuration", node.ID)
	}
	prompt := interpolateStepPrompt(node.Agent.Prompt, body.Upstream, body.Item)
	executor := &workflowAgentExecutor{s: s}
	session, err := executor.Start(ctx, workflows.AgentRequest{
		Platform: node.Agent.Platform, Directory: node.Agent.Directory, Prompt: prompt,
		Model: node.Agent.Model, Agent: node.Agent.Agent, Reasoning: node.Agent.Reasoning,
		SessionID: node.Agent.SessionID,
	})
	if err != nil {
		return workflowStepResponse{}, err
	}
	if session.Error != "" {
		return workflowStepResponse{Error: session.Error}, nil
	}
	ticker := time.NewTicker(workflowStepPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// The runner gave up or ocman is shutting down; do not leave
			// a turn running against a session nobody is watching.
			_ = executor.Cancel(context.WithoutCancel(ctx), session)
			return workflowStepResponse{}, ctx.Err()
		case <-ticker.C:
		}
		result, err := executor.Inspect(ctx, session)
		if err != nil {
			return workflowStepResponse{}, err
		}
		switch result.State {
		case "busy":
			continue
		case "error":
			return workflowStepResponse{Error: result.Error}, nil
		}
		return workflowStepResponse{OK: true, Output: agentStepOutput(result.FinalMessage)}, nil
	}
}

// agentStepOutput publishes the agent's final message as the node's
// output. Downstream references resolve through the runner's JSON path
// support, so a message that is already JSON is passed through and
// anything else is wrapped as a JSON string.
func agentStepOutput(message string) json.RawMessage {
	trimmed := strings.TrimSpace(message)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), "```"))
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// interpolateStepPrompt resolves the references a prompt may carry. The
// runner has already substituted its own variables, so only ocman's own
// forms remain.
func interpolateStepPrompt(prompt string, upstream, item json.RawMessage) string {
	if len(item) > 0 {
		prompt = substituteJSONPaths(prompt, "${item", item)
	}
	if len(upstream) == 0 {
		return prompt
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(upstream, &outputs); err != nil {
		return prompt
	}
	for id, output := range outputs {
		prompt = substituteJSONPaths(prompt, "${nodes."+id+".output", output)
	}
	return prompt
}

// substituteJSONPaths replaces every "<prefix>...}" reference with the
// value selected from payload.
func substituteJSONPaths(input, prefix string, payload json.RawMessage) string {
	for {
		start := strings.Index(input, prefix)
		if start < 0 {
			return input
		}
		end := strings.Index(input[start:], "}")
		if end < 0 {
			return input
		}
		end += start
		selector := strings.TrimPrefix(input[start+len(prefix):end], ".")
		input = input[:start] + selectJSONPath(payload, selector) + input[end+1:]
	}
}

func selectJSONPath(payload json.RawMessage, selector string) string {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return ""
	}
	if selector != "" {
		for _, segment := range strings.Split(selector, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				return ""
			}
			if value, ok = object[segment]; !ok {
				return ""
			}
		}
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
