package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
)

type backgroundTaskHost struct {
	hostsvc.Host
	reader tasksvc.Reader
}

func (h backgroundTaskHost) BackgroundTaskReader() tasksvc.Reader { return h.reader }

func TestBackgroundTaskCompositionReadsRemoteOwnersWithoutExportingTheirTasks(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	owner := tasksvc.New(statetest.Open(t, tasksvc.Schema), 1)
	if err := owner.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Stop)
	if err := owner.Do(t.Context(), tasksvc.Spec{Kind: "scan", Title: "Remote scan"}, func(context.Context, *tasksvc.Reporter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s.router().RegisterRemote("box", backgroundTaskHost{reader: owner})
	// Direct RPC exports only this server's manager, not recursively aggregated
	// work from its attached remotes.
	local, err := s.BackgroundTaskReader().List(t.Context(), "", "", 10)
	if err != nil || len(local) != 0 {
		t.Fatalf("exported foreign work: %+v %v", local, err)
	}
	handler := moduletest.Mount(s.backgroundTasksModule())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks?remoteId=box", nil))
	var tasks []tasksvc.Task
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tasks) != nil || len(tasks) != 1 || tasks[0].Title != "Remote scan" || tasks[0].OwnerID != "box" {
		t.Fatalf("response=%d %s", w.Code, w.Body)
	}
	s.router().UnregisterRemote("box")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks?remoteId=box", nil))
	if w.Code != 503 {
		t.Fatalf("disconnected owner fell back: %d %s", w.Code, w.Body)
	}
}

func TestRemotePullRequestCompositionReadsOnlyRequestedLocalPlatform(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	seedPRWatch(t, s, "fake", "s1", true)
	seedPRWatch(t, s, "other", "s1", true)
	response := s.RemotePullRequestOperation(t.Context(), "fake", platforms.PullRequestRequest{Operation: "counts"})
	var counts map[string]int
	if response.Status != 200 || json.Unmarshal(response.Body, &counts) != nil || counts["s1"] != 1 {
		t.Fatalf("counts=%+v", response)
	}
	// The local platform is authoritative; no remote adapter is needed.
	if remote, err := s.remotePRWorkflow(prworkflow.Target{Platform: "fake"}); err != nil || remote != nil {
		t.Fatalf("unexpected remote: %v %v", remote, err)
	}
}
