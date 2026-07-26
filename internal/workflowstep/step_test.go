package workflowstep

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runShim(t *testing.T, arguments []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(arguments, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// A join is pure arithmetic over the aggregate the runner already
// produced, so it must not need ocman to be reachable at all.
func TestJoinAppliesPolicyWithoutContactingOcman(t *testing.T) {
	aggregate := `{"summary":{"total":3,"succeeded":2,"failed":1},"outputs":[{"a":1},{"b":2},{}]}`
	t.Setenv("OCMAN_ENDPOINT", "http://127.0.0.1:1")
	for name, test := range map[string]struct {
		arguments []string
		wantCode  int
	}{
		"all-success fails on a failed item":  {[]string{"join", "--node", "j", "--policy", "all-success"}, 1},
		"always tolerates failures":           {[]string{"join", "--node", "j", "--policy", "always"}, 0},
		"minimum-success met":                 {[]string{"join", "--node", "j", "--policy", "minimum-success", "--min-success", "2"}, 0},
		"minimum-success unmet":               {[]string{"join", "--node", "j", "--policy", "minimum-success", "--min-success", "3"}, 1},
		"unknown policy takes strict reading": {[]string{"join", "--node", "j", "--policy", "wat"}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OCMAN_MAP_RESULT", aggregate)
			code, stdout, _ := runShim(t, test.arguments)
			if code != test.wantCode {
				t.Fatalf("exit = %d, want %d", code, test.wantCode)
			}
			// The aggregate is emitted whatever the policy decides, so a
			// failing join still records what every item produced.
			if !strings.Contains(stdout, `{"a":1}`) {
				t.Errorf("stdout did not carry item outputs: %q", stdout)
			}
		})
	}
}

func TestJoinRejectsMalformedAggregate(t *testing.T) {
	t.Setenv("OCMAN_MAP_RESULT", "not json")
	if code, _, stderr := runShim(t, []string{"join", "--node", "j", "--policy", "always"}); code == 0 ||
		!strings.Contains(stderr, "aggregate") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestShimPostsStepAndReportsOutcome(t *testing.T) {
	var received request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workflow-steps" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_ = json.NewEncoder(w).Encode(response{OK: true, Output: json.RawMessage(`{"done":true}`)})
	}))
	defer server.Close()
	t.Setenv("OCMAN_ENDPOINT", server.URL)
	t.Setenv("OCMAN_UPSTREAM", `{"build":{"tag":"v1"}}`)

	code, stdout, stderr := runShim(t, []string{"agent", "--run", "run-1", "--node", "review"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, `{"done":true}`) {
		t.Errorf("stdout = %q", stdout)
	}
	if received.Kind != "agent" || received.RunID != "run-1" || received.NodeID != "review" {
		t.Errorf("request = %+v", received)
	}
	if string(received.Upstream) != `{"build":{"tag":"v1"}}` {
		t.Errorf("upstream = %s", received.Upstream)
	}
}

// A mapped child addresses its run by parent run plus stable item key,
// because the runner creates the per-item runs itself.
func TestShimForwardsMappedItemIdentity(t *testing.T) {
	var received request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_ = json.NewEncoder(w).Encode(response{OK: true})
	}))
	defer server.Close()
	t.Setenv("OCMAN_ENDPOINT", server.URL)
	t.Setenv("OCMAN_PARENT_RUN", "run-1")
	t.Setenv("OCMAN_MAP_NODE", "items")
	t.Setenv("OCMAN_ITEM_KEY", "parser")
	t.Setenv("OCMAN_ITEM", `{"id":"parser"}`)

	if code, _, stderr := runShim(t, []string{"agent", "--node", "implement"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if received.ParentRun != "run-1" || received.ItemKey != "parser" || received.MapNode != "items" {
		t.Errorf("request = %+v", received)
	}
	if string(received.Item) != `{"id":"parser"}` {
		t.Errorf("item = %s", received.Item)
	}
}

// A false condition is reported as a non-zero exit, which the runner
// turns into a skip rather than a failure.
func TestShimReportsFalseConditionAsNonZeroExit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(response{OK: false})
	}))
	defer server.Close()
	t.Setenv("OCMAN_ENDPOINT", server.URL)
	if code, _, _ := runShim(t, []string{"condition", "--run", "r", "--node", "n", "--from", "u"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestShimRejectsIncompleteInvocation(t *testing.T) {
	for name, arguments := range map[string][]string{
		"no kind": {},
		"no node": {"agent", "--run", "r"},
		"no run":  {"agent", "--node", "n"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, _ := runShim(t, arguments); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
		})
	}
}

func TestEndpointPrefersEnvironment(t *testing.T) {
	t.Setenv("OCMAN_ENDPOINT", "http://example.test:9000/")
	if got := Endpoint(); got != "http://example.test:9000" {
		t.Fatalf("Endpoint() = %q", got)
	}
}
