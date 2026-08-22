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
	"strconv"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"

	"github.com/NoUseFreak/ocman/internal/db"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
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
	status := db.StatusBusy
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
				Parts:    []db.Part{{MessageID: "assistant", Data: json.RawMessage(`{"type":"text","text":"{\"completed\":true}"}`)}},
			}, nil
		},
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	definition := `{"id":"agent-transport","name":"Agent transport","version":"1","concurrency":1,"triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"approve","name":"Approve","type":"approval"},{"id":"agent","name":"Agent","type":"agent","agent":{"platform":"fake","directory":` + fmt.Sprintf("%q", dir) + `,"prompt":"do work"}}],"dependencies":[{"from":"approve","to":"agent"}]}`
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

	status = db.StatusWaiting
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
	if completed.State != workflows.StateSuccessful || string(completed.Nodes[1].Result.Output) != `{"completed":true}` {
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

func TestWorkflowRESTRetryFromNode(t *testing.T) {
	srv := newWorkflowTestServer(t)
	v1, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(workflowRequest))
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.workflowSvc().Start(t.Context(), v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.workflowSvc().Approve(t.Context(), run.ID, "review"); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.workflowSvc().Approve(t.Context(), run.ID, "ship"); err != nil {
		t.Fatal(err)
	}
	v2, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(strings.Replace(workflowRequest, `"name":"Ship"`, `"name":"Ship adjusted"`, 1)))
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"versionId":"` + v2.ID + `"}`)
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+run.ID+"/retry-from/ship", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
	var retried workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &retried); err != nil {
		t.Fatal(err)
	}
	if retried.RetryOfRunID != run.ID || retried.RetryFromNodeID != "ship" || retried.VersionID != v2.ID {
		t.Fatalf("retry lineage/version = %+v", retried.Run)
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

func TestWorkflowValidateActivateAndStartActive(t *testing.T) {
	srv := newWorkflowTestServer(t)
	yamlSource := `id: release
name: Release
version: "1"
concurrency: 1
nodes:
  - id: review
    name: Review
    type: approval
dependencies: []
`

	rec := httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/validate", strings.NewReader(yamlSource)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"canonicalJson"`) {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows", strings.NewReader(yamlSource)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	var version workflows.Version
	if err := json.Unmarshal(rec.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/"+version.ID+"/activate", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("activate: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/"+version.ID+"/deactivate", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"active":false`) {
		t.Fatalf("deactivate: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/"+version.ID+"/activate", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("reactivate: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.handleWorkflows(rec, httptest.NewRequest(http.MethodPost, "/api/workflows/release/start", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("start active: %d %s", rec.Code, rec.Body.String())
	}
	var run workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.VersionID != version.ID {
		t.Fatalf("active run not pinned: %+v", run.Run)
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

func TestWorkflowRESTResolvesUnknownAttempt(t *testing.T) {
	srv := newWorkflowTestServer(t)
	version, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(workflowRequest))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	run := state.WorkflowRun{ID: "unknown-rest", WorkflowID: version.WorkflowID, VersionID: version.ID, State: workflows.StateActive, CreatedAt: now, UpdatedAt: now, Nodes: []state.WorkflowNodeRun{{NodeID: "review", Type: "approval", State: workflows.NodeReady}, {NodeID: "ship", Type: "approval", State: workflows.NodePending}}}
	if err := srv.stateDB.InsertWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	stored, err := srv.stateDB.GetWorkflowRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := stored.Nodes[0].Attempts[0].ID
	if claimed, err := srv.stateDB.ClaimWorkflowAgentAttempt(t.Context(), run.ID, "review", attemptID, "", "", []state.WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 1}}, nil, now); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	if err := srv.stateDB.MarkWorkflowAttemptUnknown(t.Context(), run.ID, "review", attemptID, "interrupted", now); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+run.ID+"/resolve-unknown/"+strconv.FormatInt(attemptID, 10), strings.NewReader(`{"resolution":"retry"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve retry: %d %s", rec.Code, rec.Body.String())
	}
	var resolved workflows.RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.State != workflows.StateActive || len(resolved.Nodes[0].Attempts) != 2 || resolved.Nodes[0].Attempts[0].ResolvedBy != "user" || resolved.Nodes[0].Attempts[1].State != workflows.AttemptWaiting {
		t.Fatalf("unknown retry response: %+v", resolved)
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
	definition := `{"id":"art","name":"Art","version":"1","concurrency":1,"triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"emit","name":"Emit","type":"approval"}],"dependencies":[]}`
	version, err := srv.workflowSvc().PublishJSON(t.Context(), []byte(definition))
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.workflowSvc().Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("produced")
	hash, err := workflows.NewBlobStore(srv.workflowBlobDir).Put(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.InsertWorkflowArtifact(t.Context(), state.WorkflowArtifact{
		ID: "artifact-1", RunID: run.ID, NodeID: "emit", Name: "log", Kind: workflows.KindText,
		ContentHash: hash, SizeBytes: int64(len(payload)), CreatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
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
	rec = httptest.NewRecorder()
	srv.handleWorkflowRuns(rec, httptest.NewRequest(http.MethodGet, "/api/workflow-runs/another-run/artifacts/"+artifacts[0].ID+"/download", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-run download: %d %s", rec.Code, rec.Body.String())
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

	// /api/workflow-steps is included because the shim endpoint drives
	// agent sessions and settles run state.
	for _, path := range []string{"/mcp", "/api/workflows", "/api/workflow-runs/run/start", "/api/workflow-steps"} {
		req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Host = "localhost:8228"
		req.Header.Set("Origin", "https://evil.example")
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("hostile origin %s: want 403, got %d", path, rec.Code)
		}
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
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["agentLoops"]; ok {
		t.Fatal("legacy loop capability must not be exposed")
	}
}

// TestWorkspaceProviderCreatesAndReusesShard drives the real host-local
// worktree shard provider end to end against a temporary git repo, proving
// it creates a shard worktree and reuses it idempotently.
func TestWorkspaceProviderCreatesAndReusesShard(t *testing.T) {
	testutil.RequireGit(t)
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
