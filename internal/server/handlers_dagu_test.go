package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

type daguStatusHost struct {
	hostsvc.Host
	result        dagu.Result
	started       dagu.Run
	gotDefinition workflows.Definition
	gotName       string
	gotRunID      string
}

func (h daguStatusHost) RemoteID() string                       { return "remote-1" }
func (h daguStatusHost) DaguStatus(context.Context) dagu.Result { return h.result }

func (h *daguStatusHost) StartDaguWorkflow(_ context.Context, definition workflows.Definition) (dagu.Run, error) {
	h.gotDefinition = definition
	return h.started, nil
}

func (h *daguStatusHost) GetDaguRun(_ context.Context, name, id string) (dagu.Run, error) {
	h.gotName, h.gotRunID = name, id
	return h.started, nil
}

func (h *daguStatusHost) CancelDaguRun(_ context.Context, name, id string) error {
	h.gotName, h.gotRunID = name, id
	return nil
}

func TestDaguStatusRoutesToOwner(t *testing.T) {
	s := New(nil, nil, "", nil, nil)
	s.router().RegisterRemote("remote-1", &daguStatusHost{result: dagu.Result{Status: dagu.Compatible, Version: "2.1.0"}})
	req := httptest.NewRequest(http.MethodGet, "/api/dagu/status?remoteId=remote-1", nil)
	rec := httptest.NewRecorder()

	s.handleDaguStatus(rec, req)

	var got dagu.Result
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || got.Status != dagu.Compatible || got.Version != "2.1.0" {
		t.Fatalf("response = %+v, err = %v", got, err)
	}
}

func TestDaguRunHandlersRouteToOwner(t *testing.T) {
	definition := workflows.Definition{ID: "release", Nodes: []workflows.Node{{ID: "build", Name: "Build", Type: "command", Command: []string{"true"}}}}
	owner := &daguStatusHost{started: dagu.Run{ID: "run-1", Name: "release", Status: "running"}}
	s := New(nil, nil, "", nil, nil)
	s.router().RegisterRemote("remote-1", owner)

	body, _ := json.Marshal(map[string]any{"remoteId": "remote-1", "definition": definition})
	rec := httptest.NewRecorder()
	s.handleDaguRuns(rec, httptest.NewRequest(http.MethodPost, "/api/dagu/runs/start", bytes.NewReader(body)))
	if rec.Code != http.StatusOK || owner.gotDefinition.ID != "release" {
		t.Fatalf("start status = %d, definition = %+v", rec.Code, owner.gotDefinition)
	}

	rec = httptest.NewRecorder()
	s.handleDaguRuns(rec, httptest.NewRequest(http.MethodGet, "/api/dagu/runs/get?remoteId=remote-1&name=release&runId=run-1", nil))
	if rec.Code != http.StatusOK || owner.gotName != "release" || owner.gotRunID != "run-1" {
		t.Fatalf("get status = %d, target = %s/%s", rec.Code, owner.gotName, owner.gotRunID)
	}

	rec = httptest.NewRecorder()
	s.handleDaguRuns(rec, httptest.NewRequest(http.MethodPost, "/api/dagu/runs/cancel", bytes.NewReader([]byte(`{"remoteId":"remote-1","name":"release","runId":"run-1"}`))))
	if rec.Code != http.StatusOK || owner.gotName != "release" || owner.gotRunID != "run-1" {
		t.Fatalf("cancel status = %d, target = %s/%s", rec.Code, owner.gotName, owner.gotRunID)
	}
}

func TestDaguRunHandlersRejectUnknownRemote(t *testing.T) {
	s := New(nil, nil, "", nil, nil)
	rec := httptest.NewRecorder()
	s.handleDaguRuns(rec, httptest.NewRequest(http.MethodGet, "/api/dagu/runs/get?remoteId=missing&name=release&runId=run-1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
