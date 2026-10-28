package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
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

func TestProjectMergeSurvivesUpstreamChanges(t *testing.T) {
	for _, changed := range []string{"source", "target", "both"} {
		t.Run(changed, func(t *testing.T) {
			srv := testServer(t)
			source := db.ProjectStats{Directory: "/source", SessionCount: 2}
			target := db.ProjectStats{Directory: "/target", SessionCount: 1}
			srv.remoteProjectsFn = func() []db.ProjectStats { return []db.ProjectStats{source, target} }
			rr := httptest.NewRecorder()
			srv.handleProjectMerge(rr, httptest.NewRequest(http.MethodPost, "/api/project/merge", strings.NewReader(`{"source":{"directory":"/source"},"target":{"directory":"/target"}}`)))
			if rr.Code != http.StatusOK {
				t.Fatalf("merge: %d %s", rr.Code, rr.Body.String())
			}
			if changed != "target" {
				source.UpstreamKeys = []string{"new-source"}
			}
			if changed != "source" {
				target.UpstreamKeys = []string{"new-target"}
			}
			projects := getProjects(t, srv)
			if projects[0].ProjectKey != projects[1].ProjectKey {
				t.Fatalf("upstream change undid merge: %+v", projects)
			}
		})
	}
}

func TestProjectMergeSurvivesMissingTargetAndMergesAgain(t *testing.T) {
	srv := testServer(t)
	projects := []db.ProjectStats{{Directory: "/source"}, {Directory: "/target", RemoteID: "box"}, {Directory: "/next"}}
	srv.remoteProjectsFn = func() []db.ProjectStats { return append([]db.ProjectStats(nil), projects...) }
	merge := func(body string) {
		t.Helper()
		rr := httptest.NewRecorder()
		srv.handleProjectMerge(rr, httptest.NewRequest(http.MethodPost, "/api/project/merge", strings.NewReader(body)))
		if rr.Code != http.StatusOK {
			t.Fatalf("merge: %d %s", rr.Code, rr.Body.String())
		}
	}
	merge(`{"source":{"directory":"/source"},"target":{"directory":"/target","remoteId":"box"}}`)
	projects = []db.ProjectStats{projects[0], projects[2]}
	got := getProjects(t, srv)
	if got[0].ProjectKey != "dir:box:/target" || got[0].MergedIntoDirectory != "/target" {
		t.Fatalf("missing target erased merge: %+v", got)
	}
	merge(`{"source":{"directory":"/target","remoteId":"box"},"target":{"directory":"/next"}}`)
	got = getProjects(t, srv)
	if got[0].ProjectKey != got[1].ProjectKey || got[0].MergedIntoDirectory != "/next" {
		t.Fatalf("chained merge lost source: %+v", got)
	}
}

func TestProjectMergeCombinesCrossedUpstreamGroups(t *testing.T) {
	srv := testServer(t)
	projects := []db.ProjectStats{{Directory: "/a"}, {Directory: "/b"}, {Directory: "/c"}, {Directory: "/d"}}
	srv.remoteProjectsFn = func() []db.ProjectStats { return append([]db.ProjectStats(nil), projects...) }
	for _, body := range []string{
		`{"source":{"directory":"/a"},"target":{"directory":"/b"}}`,
		`{"source":{"directory":"/c"},"target":{"directory":"/d"}}`,
	} {
		rr := httptest.NewRecorder()
		srv.handleProjectMerge(rr, httptest.NewRequest(http.MethodPost, "/api/project/merge", strings.NewReader(body)))
		if rr.Code != http.StatusOK {
			t.Fatalf("merge: %d %s", rr.Code, rr.Body.String())
		}
	}
	projects[0].UpstreamKeys, projects[3].UpstreamKeys = []string{"x"}, []string{"x"}
	projects[1].UpstreamKeys, projects[2].UpstreamKeys = []string{"y"}, []string{"y"}
	for range 2 {
		got := getProjects(t, srv)
		for _, p := range got {
			if p.ProjectKey != got[0].ProjectKey || p.MergedIntoDirectory != "/b" {
				t.Fatalf("crossed upstreams split saved merges: %+v", got)
			}
		}
		for i, j := 0, len(projects)-1; i < j; i, j = i+1, j-1 {
			projects[i], projects[j] = projects[j], projects[i]
		}
	}
}
