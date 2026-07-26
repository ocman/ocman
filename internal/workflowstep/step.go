// Package workflowstep implements `ocman workflow-step`, the command an
// external runner executes for the node types it cannot run itself.
//
// Dagu only runs shell commands. Agent, approval, and conditional nodes
// need ocman's session service, approval state, and CEL evaluator, so
// the compiled spec invokes this shim instead of trying to express them
// in YAML. Keeping the real node configuration behind the shim also
// means prompts and credentials never reach the spec or the runner's
// on-disk step logs.
package workflowstep

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Endpoint returns the ocman API base URL the shim talks to. The Dagu
// process is started by ocman, which seeds this into its environment.
func Endpoint() string {
	if endpoint := os.Getenv("OCMAN_ENDPOINT"); endpoint != "" {
		return strings.TrimRight(endpoint, "/")
	}
	return "http://127.0.0.1:8229"
}

type request struct {
	Kind      string          `json:"kind"`
	RunID     string          `json:"runId,omitempty"`
	NodeID    string          `json:"nodeId"`
	From      string          `json:"from,omitempty"`
	ParentRun string          `json:"parentRunId,omitempty"`
	MapNode   string          `json:"mapNodeId,omitempty"`
	ItemKey   string          `json:"itemKey,omitempty"`
	Item      json.RawMessage `json:"item,omitempty"`
	Upstream  json.RawMessage `json:"upstream,omitempty"`
}

type response struct {
	OK     bool            `json:"ok"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Run executes one shim invocation and returns the process exit code.
// Non-zero tells the runner the step failed, which is also how a false
// condition is reported: Dagu treats a non-zero precondition as "skip".
func Run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		fmt.Fprintln(stderr, "workflow-step requires a kind")
		return 2
	}
	kind := arguments[0]
	flags := flag.NewFlagSet("workflow-step "+kind, flag.ContinueOnError)
	flags.SetOutput(stderr)
	runID := flags.String("run", "", "ocman run id")
	nodeID := flags.String("node", "", "ocman node id")
	from := flags.String("from", "", "upstream node id for a condition")
	policy := flags.String("policy", "", "join policy")
	minSuccess := flags.Int("min-success", 0, "minimum successful items for a minimum-success join")
	if err := flags.Parse(arguments[1:]); err != nil {
		return 2
	}
	if *nodeID == "" {
		fmt.Fprintln(stderr, "workflow-step requires --node")
		return 2
	}
	// A join is pure arithmetic over the aggregate the runner already
	// produced, so it needs no round trip to ocman.
	if kind == "join" {
		return runJoin(*policy, *minSuccess, os.Getenv("OCMAN_MAP_RESULT"), stdout, stderr)
	}
	body := request{Kind: kind, RunID: *runID, NodeID: *nodeID, From: *from,
		ParentRun: os.Getenv("OCMAN_PARENT_RUN"), MapNode: os.Getenv("OCMAN_MAP_NODE"),
		ItemKey: os.Getenv("OCMAN_ITEM_KEY")}
	if item := os.Getenv("OCMAN_ITEM"); json.Valid([]byte(item)) {
		body.Item = json.RawMessage(item)
	}
	if upstream := os.Getenv("OCMAN_UPSTREAM"); json.Valid([]byte(upstream)) {
		body.Upstream = json.RawMessage(upstream)
	}
	if body.RunID == "" && body.ParentRun == "" {
		fmt.Fprintln(stderr, "workflow-step requires --run or a mapped parent run")
		return 2
	}
	result, err := post(Endpoint(), body)
	if err != nil {
		fmt.Fprintf(stderr, "workflow-step %s: %v\n", kind, err)
		return 1
	}
	if len(result.Output) > 0 {
		fmt.Fprintln(stdout, string(result.Output))
	}
	if !result.OK {
		if result.Error != "" {
			fmt.Fprintf(stderr, "workflow-step %s: %s\n", kind, result.Error)
		}
		return 1
	}
	return 0
}

// joinAggregate is the shape Dagu's parallel step emits. Item outputs
// keep input order, which is what the join contract promises.
type joinAggregate struct {
	Summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"summary"`
	Outputs []json.RawMessage `json:"outputs"`
}

func runJoin(policy string, minSuccess int, raw string, stdout, stderr io.Writer) int {
	var aggregate joinAggregate
	if err := json.Unmarshal([]byte(raw), &aggregate); err != nil {
		fmt.Fprintf(stderr, "workflow-step join: map result is not a Dagu aggregate: %v\n", err)
		return 1
	}
	outputs, err := json.Marshal(aggregate.Outputs)
	if err != nil {
		fmt.Fprintf(stderr, "workflow-step join: %v\n", err)
		return 1
	}
	// The aggregate is emitted whatever the policy decides, so a failing
	// join still records what every item produced.
	fmt.Fprintln(stdout, string(outputs))
	if joinSucceeds(policy, minSuccess, aggregate) {
		return 0
	}
	fmt.Fprintf(stderr, "workflow-step join: policy %q not met (%d/%d succeeded)\n",
		policy, aggregate.Summary.Succeeded, aggregate.Summary.Total)
	return 1
}

func joinSucceeds(policy string, minSuccess int, aggregate joinAggregate) bool {
	switch policy {
	case "always":
		return true
	case "minimum-success":
		return aggregate.Summary.Succeeded >= minSuccess
	default:
		// all-success, and any unknown policy, take the strict reading.
		return aggregate.Summary.Failed == 0
	}
}

// post sends the step to ocman and waits. An agent node can run for a
// long time, so the client sets no overall timeout and relies on the
// loopback connection plus the runner's own step timeout.
//
// ponytail: no heartbeat on the wire. Add one if a step ever has to
// survive a proxy that closes idle connections.
func post(endpoint string, body request) (response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return response{}, err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint+"/api/workflow-steps", bytes.NewReader(encoded))
	if err != nil {
		return response{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(request)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return response{}, fmt.Errorf("ocman returned %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var result response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return response{}, err
	}
	return result, nil
}
