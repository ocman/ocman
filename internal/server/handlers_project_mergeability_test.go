package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge/github"
)

func TestProjectPRMergeability(t *testing.T) {
	for _, value := range []string{"true", "false", "null", "error"} {
		t.Run(value, func(t *testing.T) {
			srv := testServer(t)
			dir := initGitHubRepo(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/alice/myproj/pulls/42" {
					t.Errorf("unexpected lookup: %s", r.URL.Path)
				}
				if value == "error" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				_, _ = w.Write([]byte(`{"mergeable":` + value + `}`))
			}))
			defer upstream.Close()
			srv.integrations.GitHub = github.NewForTest(upstream.URL, "test-token", upstream.Client())
			req := httptest.NewRequest(http.MethodGet, "/api/project/pr-mergeability?dir="+url.QueryEscape(dir)+"&remoteId=local&remote=origin&number=42", nil)
			rr := httptest.NewRecorder()
			srv.handleProjectPRMergeability(rr, req)
			if value == "error" {
				if rr.Code != http.StatusBadGateway {
					t.Fatalf("status: %d", rr.Code)
				}
			} else if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"mergeable":`+value) {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestProjectPRMergeabilityInvalidTarget(t *testing.T) {
	srv := testServer(t)
	dir := url.QueryEscape(initGitHubRepo(t))
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"number=-1", http.StatusBadRequest},
		{"number=abc", http.StatusBadRequest},
		{"number=42&dir=relative&remote=origin&remoteId=local", http.StatusBadRequest},
		{"number=42&dir=" + dir + "&remote=origin", http.StatusBadRequest},
		{"number=42&dir=" + dir + "&remote=nope&remoteId=local", http.StatusNotFound},
		{"number=42&dir=" + dir + "&remote=origin&remoteId=missing", http.StatusServiceUnavailable},
	} {
		rr := httptest.NewRecorder()
		srv.handleProjectPRMergeability(rr, httptest.NewRequest(http.MethodGet, "/api/project/pr-mergeability?"+tc.query, nil))
		if rr.Code != tc.status {
			t.Errorf("%s: status=%d want=%d body=%s", tc.query, rr.Code, tc.status, rr.Body.String())
		}
	}
}
