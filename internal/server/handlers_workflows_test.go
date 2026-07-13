package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

const workflowRequest = `{
	"id":"release","name":"Release","version":"1","concurrency":1,
	"nodes":[{"id":"review","name":"Review","type":"approval"},{"id":"ship","name":"Ship","type":"approval"}],
	"dependencies":[{"from":"review","to":"ship"}]
}`

func newWorkflowTestServer(t *testing.T) *Server {
	t.Helper()
	return New(nil, openWatcherTestStateDB(t), "", nil, nil)
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
	select {
	case event := <-sub.ch:
		if event.event != "workflow.run.updated" || !strings.Contains(string(event.data), run.ID) {
			t.Fatalf("unexpected SSE event: %+v", event)
		}
	default:
		t.Fatal("start did not broadcast workflow.run.updated")
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
