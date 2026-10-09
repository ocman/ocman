package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestFactoryIssueTransfer(t *testing.T) {
	var created map[string]string
	status := http.StatusCreated
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/repo/issues" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"number":9,"title":"Gap","state":"open","html_url":"https://github.com/acme/repo/issues/9"}`))
	}))
	defer api.Close()
	svc := &fakeFactoryService{
		epics:  []factory.WorkEpic{{ID: "epic", Goal: "Ship", InitialProject: "/repo"}},
		issues: []factory.Issue{{ID: "epic.1.3.4", Kind: "task", Status: "open", Title: "Gap", Description: "Cover it."}, {ID: "epic.1.3.5", Kind: "task", Status: "in_progress", Title: "Busy"}},
	}
	srv := New(nil, nil, "", platforms.NewRegistry(), nil)
	srv.factory = svc
	srv.integrations.GitHub, srv.integrations.Forgejo = nil, nil // never reach a real forge
	host := &factoryImplementationHost{upstreams: hostsvc.ProjectUpstreams{Remotes: []forge.Remote{{Name: "origin", Type: forge.RemoteTypeGitHub, Repo: "acme/repo"}}}}
	srv.hostRouter = hostsvc.NewRouter(host)
	post := func(issue string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/factory/epics/epic/issues/"+issue+"/transfer", nil)
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		srv.handleFactoryEpic(rec, req)
		return rec
	}

	if rec := post("epic.1.3.4"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no supported issue tracker") {
		t.Fatalf("no tracker configured = %d %s", rec.Code, rec.Body)
	}
	srv.integrations.GitHub = github.NewForTest(api.URL, "token", api.Client())
	if rec := post("epic.1.3.5"); rec.Code != http.StatusBadRequest || created != nil {
		t.Fatalf("in-progress transfer = %d", rec.Code)
	}
	rec := post("epic.1.3.4")
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "issues/9") {
		t.Fatalf("transfer = %d %s", rec.Code, rec.Body)
	}
	if created["title"] != "Gap" || !strings.Contains(created["body"], "Cover it.") || !strings.Contains(created["body"], "Epic epic (Ship), Issue epic.1.3.4") {
		t.Fatalf("tracker issue = %#v", created)
	}
	if len(svc.comments) != 1 || svc.comments[0].Body != "Transferred to https://github.com/acme/repo/issues/9" || !reflect.DeepEqual(svc.mutation, factory.GraphMutation{Action: "delete", EpicID: "epic", IssueID: "epic.1.3.4", Actor: "user"}) {
		t.Fatalf("comment %#v, mutation %#v", svc.comments, svc.mutation)
	}
	status = http.StatusForbidden
	if rec := post("epic.1.3.4"); rec.Code != http.StatusBadGateway {
		t.Fatalf("forge failure = %d", rec.Code)
	}
}
