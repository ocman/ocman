package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"

	"github.com/NoUseFreak/ocman/internal/db"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

const workflowRequest = `{
	"id":"release","name":"Release","version":"1","concurrency":1,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[{"id":"review","name":"Review","type":"approval"},{"id":"ship","name":"Ship","type":"approval"}],
	"dependencies":[{"from":"review","to":"ship"}]
}`

func newWorkflowTestServer(t *testing.T) *Server {
	t.Helper()
	return New(nil, openWatcherTestStateDB(t), "", nil, nil)
}

func TestWorkflowRESTApprovalToAgentSession(t *testing.T) {
	dir := t.TempDir()
	status := "busy"
	var sent platforms.SendMessageRequest
	p := &fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "agent-session", Platform: "fake"}},
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			if req.Directory != dir {
				t.Fatalf("create directory: %q", req.Directory)
			}
			return &platforms.CreateSessionResponse{ID: "agent-session"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			sent = req
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session:  &db.Session{ID: id, Platform: "fake", Directory: dir, Status: status},
				Messages: []db.Message{{ID: "assistant", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant"}`)}},
				Parts:    []db.Part{{MessageID: "assistant", Data: json.RawMessage(`{"type":"text","text":"completed work"}`)}},
			}, nil
		},
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	definition := `{"id":"agent-transport","name":"Agent transport","version":"1","concurrency":1,"triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"approve","name":"Approve","type":"approval"},{"id":"agent","name":"Agent","type":"agent","agent":{"platform":"fake","directory":` + fmt.Sprintf("%q", dir) + `,"prompt":"do work","collectors":[{"name":"message","type":"final-message"}]}}],"dependencies":[{"from":"approve","to":"agent"}]}`
	version, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.workflowSvc().Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+run.ID+"/approve/approve", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	if sent.SessionID != "agent-session" || sent.Message != "do work" {
		t.Fatalf("agent prompt not sent through session service: %+v", sent)
	}
	var running workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &running); err != nil {
		t.Fatal(err)
	}
	if attempt := running.Nodes[1].Attempts[0]; attempt.SessionID != "agent-session" || attempt.SessionState != "busy" {
		t.Fatalf("transport omitted session link/state: %+v", attempt)
	}

	status = "waiting"
	if err := srv.workflowSvc().Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var completed workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	if completed.State != workflows.StateSuccessful || string(completed.Nodes[1].Attempts[0].Outputs["message"]) != `"completed work"` {
		t.Fatalf("idle completion missing from transport: %+v", completed)
	}
}

func TestWorkflowRESTLifecycleAndSSE(t *testing.T) {
	srv := newWorkflowTestServer(t)
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	rec := httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows", strings.NewReader(workflowRequest)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	var version workflows.Version
	if err := json.Unmarshal(rec.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/"+version.ID+"/runs", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var run workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	seenTrigger, seenRun := false, false
	for range 2 {
		select {
		case event := <-sub.ch:
			seenTrigger = seenTrigger || event.event == "workflow.trigger.updated"
			seenRun = seenRun || event.event == "workflow.run.updated" && strings.Contains(string(event.data), run.ID)
		default:
			t.Fatal("start did not broadcast workflow updates")
		}
	}
	if !seenTrigger || !seenRun {
		t.Fatalf("missing workflow SSE events: trigger=%v run=%v", seenTrigger, seenRun)
	}

	for _, request := range []struct {
		path string
		want string
	}{
		{"/api/workflow-runs/" + run.ID + "/approve/review", workflows.StateActive},
		{"/api/workflow-runs/" + run.ID + "/approve/ship", workflows.StateSuccessful},
	} {
		rec = httptest.NewRecorder()
		srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodPost, request.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", request.path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.State != request.want {
			t.Fatalf("%s: want %s, got %s", request.path, request.want, run.State)
		}
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Nodes) != 2 || len(run.Nodes[0].Attempts) != 1 || len(run.Nodes[1].Attempts) != 1 {
		t.Fatalf("missing durable history: %+v", run.Nodes)
	}
}

func TestWorkflowPublishLimitsBody(t *testing.T) {
	srv := newWorkflowTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows", strings.NewReader(strings.Repeat(" ", maxRequestBody+1))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestWorkflowRESTPauseAndCancel(t *testing.T) {
	srv := newWorkflowTestServer(t)
	version, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(workflowRequest))
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.workflowSvc().Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"pause", "cancel"} {
		rec := httptest.NewRecorder()
		srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+run.ID+"/"+action, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body.String())
		}
	}
}

func TestWorkflowMCPAndRESTShareServiceContract(t *testing.T) {
	srv := newWorkflowTestServer(t)
	tools := internalmcp.ServerTools(internalmcp.Deps{StateDB: srv.stateDB, WorkflowService: srv.workflowSvc()})
	mcpServer, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatal(err)
	}
	defer mcpServer.Close()
	call := func(name string, args map[string]interface{}) map[string]interface{} {
		t.Helper()
		req := mcplib.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		result, err := mcpServer.Client().CallTool(context.Background(), req)
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v, %v", name, result, err)
		}
		text := result.Content[0].(mcplib.TextContent).Text
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	published := call("publish_workflow", map[string]interface{}{"definition": workflowRequest})
	versionID := published["version_id"].(string)
	rec := httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/"+versionID+"/runs", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("REST start: %d %s", rec.Code, rec.Body.String())
	}
	var run workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if got := call("inspect_workflow_run", map[string]interface{}{"run_id": run.ID}); got["version_id"] != versionID {
		t.Fatalf("MCP did not observe REST run: %#v", got)
	}
	if got := call("pause_workflow_run", map[string]interface{}{"run_id": run.ID}); got["state"] != workflows.StatePaused {
		t.Fatalf("MCP pause: %#v", got)
	}
	rec = httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+run.ID+"/resume", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("REST resume: %d %s", rec.Code, rec.Body.String())
	}
	if got := call("inspect_workflow_run", map[string]interface{}{"run_id": run.ID}); got["state"] != workflows.StateActive {
		t.Fatalf("MCP did not observe REST resume: %#v", got)
	}
}

func TestWorkflowArtifactRESTListAndDownload(t *testing.T) {
	srv := newWorkflowTestServer(t)
	srv.workflowBlobDir = filepath.Join(t.TempDir(), "artifacts")
	dir := t.TempDir()
	definition := `{"id":"art","name":"Art","version":"1","concurrency":1,"directory":` + fmt.Sprintf("%q", dir) +
		`,"triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"emit","name":"Emit","type":"command",` +
		`"command":["/bin/sh","-c","printf produced"],"permission":[{"permission":"bash","pattern":"/bin/sh -c *","action":"allow"}],` +
		`"outputs":[{"name":"log","type":"text"}]}],"dependencies":[]}`
	version, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.workflowSvc().Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Command runs asynchronously; wait for success.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := srv.workflowSvc().GetRun(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == workflows.StateSuccessful {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	rec := httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/artifacts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list artifacts: %d %s", rec.Code, rec.Body.String())
	}
	var artifacts []workflows.Artifact
	if err := json.Unmarshal(rec.Body.Bytes(), &artifacts); err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Name != "log" || !artifacts[0].PayloadAvailable {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/artifacts/"+artifacts[0].ID+"/download", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "produced") {
		t.Fatalf("download body missing payload: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Disposition") == "" {
		t.Fatal("download missing Content-Disposition")
	}

	// Missing artifact → 404.
	rec = httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/artifacts/nope/download", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing artifact: want 404, got %d", rec.Code)
	}
}

func TestWorkflowMCPRouteIsLocalhostOnly(t *testing.T) {
	srv := newWorkflowTestServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote MCP request: want 403, got %d", rec.Code)
	}
}

func TestCapabilitiesExposeWorkflowsWithoutPlatform(t *testing.T) {
	srv := newWorkflowTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Workflows struct {
			Enabled bool `json:"enabled"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Workflows.Enabled {
		t.Fatal("workflows should depend on state DB, not a platform")
	}
}

// TestWorkspaceProviderCreatesAndReusesShard drives the real host-local
// worktree shard provider end to end against a temporary git repo, proving
// it creates a shard worktree and reuses it idempotently.
func TestWorkspaceProviderCreatesAndReusesShard(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, "PATH="+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	var provider workflowWorkspaceProvider
	path, err := provider.EnsureShard(context.Background(), "run-1", repo, 0)
	if err != nil {
		t.Fatalf("EnsureShard: %v", err)
	}
	if path == "" {
		t.Fatal("empty shard path")
	}
	reused, err := provider.EnsureShard(context.Background(), "run-1", repo, 0)
	if err != nil || reused != path {
		t.Fatalf("EnsureShard not idempotent: %q vs %q (%v)", reused, path, err)
	}
	if _, err := provider.EnsureShard(context.Background(), "run-1", "", 0); err == nil {
		t.Fatal("expected error for empty repo directory")
	}
}
