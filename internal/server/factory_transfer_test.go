package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type transferFactoryFake struct {
	fakeFactoryService
	issue     factory.Issue
	finishErr error
	cancelled string
}

func (f *transferFactoryFake) CancelIssueTransfer(_ context.Context, _, issueID string) error {
	f.cancelled = issueID
	return f.err
}

func (f *transferFactoryFake) TransferIssue(ctx context.Context, _, _ string, create func(context.Context, factory.Issue) (string, error)) (string, error) {
	url, err := create(ctx, f.issue)
	if err != nil {
		return "", err
	}
	return url, f.finishErr
}

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
	svc := &transferFactoryFake{
		fakeFactoryService: fakeFactoryService{epics: []factory.WorkEpic{{ID: "epic", Goal: "Ship", InitialProject: "/repo"}}},
		issue:              factory.Issue{ID: "epic.1.3.4", Kind: "task", Status: "open", Title: "Gap", Description: "Cover it."},
	}
	srv := New(nil, nil, "", platforms.NewRegistry(), nil)
	useFactory(srv, svc)
	srv.integrations.GitHub, srv.integrations.Forgejo = nil, nil // never reach a real forge
	host := &factoryImplementationHost{upstreams: hostsvc.ProjectUpstreams{Remotes: []forge.Remote{{Name: "origin", Type: forge.RemoteTypeGitHub, Repo: "acme/repo"}}}}
	srv.hostRouter = hostsvc.NewRouter(host)
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/factory/epics/epic/issues/epic.1.3.4/transfer", nil)
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		serveFactory(srv, rec, req)
		return rec
	}

	if rec := post(); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no supported issue tracker") {
		t.Fatalf("no tracker configured = %d %s", rec.Code, rec.Body)
	}
	srv.integrations.GitHub = github.NewWithCredentials(api.URL, "token", api.Client())
	rec := post()
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "issues/9") {
		t.Fatalf("transfer = %d %s", rec.Code, rec.Body)
	}
	if created["title"] != "Gap" || !strings.Contains(created["body"], "Cover it.") || !strings.Contains(created["body"], "Epic epic (Ship), Issue epic.1.3.4") {
		t.Fatalf("tracker issue = %#v", created)
	}
	svc.finishErr = errors.New("db locked")
	if rec := post(); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "retry to resume") {
		t.Fatalf("partial transfer = %d %s", rec.Code, rec.Body)
	}
	svc.finishErr = nil
	status = http.StatusForbidden
	if rec := post(); rec.Code != http.StatusBadGateway {
		t.Fatalf("forge failure = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/epics/epic/issues/epic.1.3.4/transfer/cancel", nil)
	req.RemoteAddr = "127.0.0.1:1"
	cancel := httptest.NewRecorder()
	serveFactory(srv, cancel, req)
	if cancel.Code != http.StatusNoContent || svc.cancelled != "epic.1.3.4" {
		t.Fatalf("cancel = %d, %q", cancel.Code, svc.cancelled)
	}
}
