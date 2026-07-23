package dagu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

func commandDefinition() workflows.Definition {
	return workflows.Definition{
		ID: "release", Name: "Release", Directory: "/repo",
		Triggers: []workflows.Trigger{{ID: "manual", Type: workflows.TriggerManual}},
		Nodes: []workflows.Node{
			{ID: "build", Name: "Build", Type: "command", Command: []string{"printf", "%s", "hello world"}, Environment: map[string]string{"MODE": "test"}},
			{ID: "ship", Name: "Ship", Type: "command", Command: []string{"./ship"}},
		},
		Dependencies: []workflows.Dependency{{From: "build", To: "ship"}},
	}
}

func TestClientStartUsesInlineSpecAndStableRunID(t *testing.T) {
	var body struct {
		Spec     string `json:"spec"`
		Name     string `json:"name"`
		DAGRunID string `json:"dagRunId"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/dag-runs" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"dagRunId": body.DAGRunID})
	}))
	defer server.Close()

	run, err := NewClient(server.URL, server.Client()).Start(context.Background(), commandDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if run.ID == "" || run.ID != body.DAGRunID || run.Name != "release" || body.Name != "release" {
		t.Fatalf("run = %+v, request = %+v", run, body)
	}
	for _, want := range []string{"working_dir: /repo", "name: Build", "run: printf %s 'hello world'", "MODE: test", "depends:", "- Build"} {
		if !strings.Contains(body.Spec, want) {
			t.Errorf("spec missing %q:\n%s", want, body.Spec)
		}
	}
}

func TestClientRejectsNonCommandWorkflow(t *testing.T) {
	definition := commandDefinition()
	definition.Nodes[0].Type = "agent"
	if _, err := NewClient("http://unused", http.DefaultClient).Start(context.Background(), definition); err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("error = %v", err)
	}

	definition = commandDefinition()
	definition.Triggers = append(definition.Triggers, workflows.Trigger{ID: "cron", Type: workflows.TriggerCron})
	if _, err := NewClient("http://unused", http.DefaultClient).Start(context.Background(), definition); err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("error = %v", err)
	}

	definition = commandDefinition()
	definition.Dependencies[0].Condition = "failed"
	if _, err := NewClient("http://unused", http.DefaultClient).Start(context.Background(), definition); err == nil || !strings.Contains(err.Error(), "unconditional") {
		t.Fatalf("error = %v", err)
	}

	definition = commandDefinition()
	definition.FailFast = true
	if _, err := NewClient("http://unused", http.DefaultClient).Start(context.Background(), definition); err == nil || !strings.Contains(err.Error(), "fail-fast") {
		t.Fatalf("error = %v", err)
	}

	definition = commandDefinition()
	definition.Nodes[0].Command = []string{"echo", "${nodes.build.output}"}
	if _, err := NewClient("http://unused", http.DefaultClient).Start(context.Background(), definition); err == nil || !strings.Contains(err.Error(), "interpolation") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientReadsDaguGraphAndStepLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/dag-runs/release/run-1":
			_, _ = io.WriteString(w, `{"dagRunDetails":{"dagRunId":"run-1","name":"release","statusLabel":"running","startedAt":"2026-07-23T10:00:00Z","finishedAt":"","nodes":[{"step":{"name":"Build","depends":[]},"statusLabel":"succeeded","startedAt":"2026-07-23T10:00:00Z","finishedAt":"2026-07-23T10:00:01Z"},{"step":{"name":"Ship","depends":["Build"]},"statusLabel":"running","startedAt":"2026-07-23T10:00:01Z","finishedAt":""}]}}`)
		case "/api/v1/dag-runs/release/run-1/steps/Build/log":
			if r.URL.Query().Get("tail") != "1000" {
				t.Errorf("tail = %q", r.URL.Query().Get("tail"))
			}
			_, _ = io.WriteString(w, `{"content":"built\n"}`)
		case "/api/v1/dag-runs/release/run-1/steps/Ship/log":
			_, _ = io.WriteString(w, `{"content":"shipping\n"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	run, err := NewClient(server.URL, server.Client()).GetRun(context.Background(), "release", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" || len(run.Nodes) != 2 || run.Nodes[0].Log != "built\n" || run.Nodes[1].Log != "shipping\n" || len(run.Nodes[1].Depends) != 1 {
		t.Fatalf("run = %+v", run)
	}
}

func TestClientToleratesMissingPendingStepLogAndCancels(t *testing.T) {
	var stopped bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/dag-runs/release/run-1":
			_, _ = io.WriteString(w, `{"dagRunDetails":{"dagRunId":"run-1","name":"release","statusLabel":"running","nodes":[{"step":{"name":"Build"},"statusLabel":"not_started"}]}}`)
		case "/api/v1/dag-runs/release/run-1/steps/Build/log":
			http.NotFound(w, r)
		case "/api/v1/dag-runs/release/run-1/stop":
			stopped = r.Method == http.MethodPost
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	if _, err := client.GetRun(context.Background(), "release", "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel(context.Background(), "release", "run-1"); err != nil || !stopped {
		t.Fatalf("Cancel() err = %v, stopped = %v", err, stopped)
	}
}
