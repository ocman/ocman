package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestProjectMergeKeepsCheckoutsAndIncludesRemoteMembers(t *testing.T) {
	srv := testServer(t)
	srv.remoteProjectsFn = func() []db.ProjectStats {
		return []db.ProjectStats{
			{Directory: "/source", SessionCount: 2, UpstreamKeys: []string{"source"}},
			{Directory: "/clone", RemoteID: "box", SessionCount: 3, UpstreamKeys: []string{"source"}},
			{Directory: "/target", SessionCount: 1},
			{Directory: "/source", RemoteID: "unrelated", SessionCount: 4},
		}
	}
	merge := func(body string, want int) {
		t.Helper()
		rr := httptest.NewRecorder()
		srv.handleProjectMerge(rr, httptest.NewRequest(http.MethodPost, "/api/project/merge", strings.NewReader(body)))
		if rr.Code != want {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
	}
	merge(`{"source":{"directory":"/source"},"target":{"directory":"/target"}}`, http.StatusOK)
	projects := getProjects(t, srv)
	if len(projects) != 4 {
		t.Fatalf("lost checkout rows: %v", projects)
	}
	for _, p := range projects {
		if p.RemoteID == "unrelated" {
			if p.MergedIntoDirectory != "" {
				t.Fatal("merged unrelated owner")
			}
			continue
		}
		if p.ProjectKey != "dir:local:/target" {
			t.Fatalf("not grouped into target: %+v", p)
		}
		if p.Directory != "/target" && p.MergedIntoDirectory != "/target" {
			t.Fatalf("missing representative: %+v", p)
		}
	}
	merge(`{"source":{"directory":"/target"},"target":{"directory":"/clone","remoteId":"box"}}`, http.StatusBadRequest)
	merge(`{"source":{"directory":"/missing"},"target":{"directory":"/target"}}`, http.StatusBadRequest)
	merge(`{`, http.StatusBadRequest)
}
