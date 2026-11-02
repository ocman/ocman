package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
)

func TestProjectsAPIReturnsOnlyFinalProjects(t *testing.T) {
	srv := testServer(t)
	srv.remoteProjectsFn = func() []db.ProjectStats {
		return []db.ProjectStats{
			{Directory: "/source", SessionCount: 2, MessageCount: 3, TotalTokensIn: 4, TotalTokensOut: 5, TotalCost: 6, LastUsed: 10},
			{Directory: "/target", RemoteID: "box", RemoteName: "Box", Platform: "r-box:opencode", SessionCount: 7, LastUsed: 9},
		}
	}
	rr := httptest.NewRecorder()
	serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/project/merge", strings.NewReader(`{"source":{"directory":"/source"},"target":{"directory":"/target","remoteId":"box"}}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rr.Code, rr.Body.String())
	}
	projects := getProjects(t, srv)
	if len(projects) != 1 || projects[0].Directory != "/target" || projects[0].RemoteID != "box" {
		t.Fatalf("expected only the final project, got %+v", projects)
	}
	p := projects[0]
	if p.SessionCount != 9 || p.MessageCount != 3 || p.TotalTokensIn != 4 || p.TotalTokensOut != 5 || p.TotalCost != 6 || p.LastUsed != 10 || p.Platform != "r-box:opencode" {
		t.Fatalf("lost aggregates or target ownership: %+v", p)
	}
	rr = httptest.NewRecorder()
	serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	var wire []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mergedIntoDirectory", "mergedIntoRemoteId", "associatedDirectory"} {
		if _, ok := wire[0][key]; ok {
			t.Fatalf("leaked internal checkout metadata %q: %s", key, rr.Body.String())
		}
	}
}

func TestResolveProjectIdentitiesOnlyReturnsFinals(t *testing.T) {
	srv := testServer(t)
	projects := []db.ProjectStats{{Directory: "/local/repo", UpstreamKeys: []string{"shared"}}, {Directory: "/remote/clone", RemoteID: "box", UpstreamKeys: []string{"shared"}}, {Directory: "/target", RemoteID: "gone"}}
	srv.remoteProjectsFn = func() []db.ProjectStats { return append([]db.ProjectStats(nil), projects...) }
	remote.AssignProjectKeys(projects)
	if err := srv.stateDB.MergeProjects(t.Context(), []string{state.ProjectMergeKey("local", "/local/repo")}, state.ProjectMergeTarget{Key: state.ProjectMergeKey("gone", "/target"), Directory: "/target", RemoteID: "gone"}); err != nil {
		t.Fatal(err)
	}
	projects = projects[:2] // The explicit target is now disconnected.
	rr := httptest.NewRecorder()
	serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/projects/resolve", strings.NewReader(`{"checkouts":[{"directory":"/local/.worktrees/repo/topic"},{"directory":"/remote/clone","remoteId":"box"},{"directory":"/target","remoteId":"gone"},{"directory":"/local/repo","remoteId":"unrelated"}]}`)))
	var identities []struct{ Key, Directory, RemoteID, Platform string }
	if err := json.Unmarshal(rr.Body.Bytes(), &identities); err != nil || rr.Code != http.StatusOK || len(identities) != 4 {
		t.Fatalf("resolve: %d %s, %v", rr.Code, rr.Body.String(), err)
	}
	for _, identity := range identities[:3] {
		if identity.Key != "dir:gone:/target" || identity.Directory != "/target" || identity.RemoteID != "gone" || identity.Platform != "" {
			t.Fatalf("not a final identity: %+v", identity)
		}
	}
	if identities[3].Key == identities[0].Key || identities[3].Directory != "/local/repo" || identities[3].RemoteID != "unrelated" {
		t.Fatalf("unknown owner was conflated: %+v", identities[3])
	}
	if got := getProjects(t, srv); len(got) != 1 || got[0].Directory != "/target" {
		t.Fatalf("disconnected final not listed: %+v", got)
	}
}

func TestResolveProjectIdentitiesValidation(t *testing.T) {
	srv := testServer(t)
	for _, body := range []string{`{`, `{"checkouts":[{"directory":" "}]}`} {
		rr := httptest.NewRecorder()
		serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/projects/resolve", strings.NewReader(body)))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/projects/resolve", strings.NewReader(`{"checkouts":[]}`)))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("empty resolve: %d %s", rr.Code, rr.Body.String())
	}
}

func TestArchiveFinalProjectAppliesToAllPrivateMembers(t *testing.T) {
	srv := testServer(t)
	srv.remoteProjectsFn = func() []db.ProjectStats {
		return []db.ProjectStats{{Directory: "/source", UpstreamKeys: []string{"shared"}}, {Directory: "/clone", UpstreamKeys: []string{"shared"}}, {Directory: "/target"}, {Directory: "/unrelated"}}
	}
	if err := srv.stateDB.MergeProjects(t.Context(), []string{state.ProjectMergeKey("local", "/source")}, state.ProjectMergeTarget{Key: state.ProjectMergeKey("local", "/target"), Directory: "/target"}); err != nil {
		t.Fatal(err)
	}
	for _, archived := range []string{"true", "false"} {
		rr := httptest.NewRecorder()
		serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/project/archive", strings.NewReader(`{"directory":"/target","remoteId":"local","archived":`+archived+`}`)))
		if rr.Code != http.StatusOK {
			t.Fatalf("archive: %d %s", rr.Code, rr.Body.String())
		}
		markers, err := srv.stateDB.ArchivedProjects(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, directory := range []string{"/source", "/clone", "/target", "/unrelated"} {
			_, exists := markers[state.ProjectKey{RemoteID: "local", Root: directory}]
			if exists != (archived == "true" && directory != "/unrelated") {
				t.Fatalf("%s archive=%s: %+v", directory, archived, markers)
			}
		}
	}
}

func TestArchiveFinalProjectRejectsDisconnectedMemberBeforeChangingState(t *testing.T) {
	srv := testServer(t)
	local := &projectArchiveHost{projects: []db.ProjectStats{{Directory: "/local", UpstreamKeys: []string{"shared"}}}}
	srv.hostRouter = hostsvc.NewRouter(local)
	srv.remoteProjectsFn = func() []db.ProjectStats {
		return []db.ProjectStats{{Directory: "/remote", RemoteID: "gone", UpstreamKeys: []string{"shared"}}}
	}
	rr := httptest.NewRecorder()
	serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/project/archive", strings.NewReader(`{"directory":"/local","remoteId":"local","archived":true}`)))
	markers, err := srv.stateDB.ArchivedProjects(t.Context())
	if rr.Code != http.StatusServiceUnavailable || err != nil || len(markers) != 0 || local.stopped != "" {
		t.Fatalf("changed state before resolving all owners: status=%d stopped=%q markers=%+v err=%v", rr.Code, local.stopped, markers, err)
	}
}

func TestResolveProjectIdentitiesReportsInventoryErrors(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	serveModule(srv, srv.projectsModule(), rr, httptest.NewRequest(http.MethodPost, "/api/projects/resolve", strings.NewReader(`{"checkouts":[{"directory":"/repo"}]}`)))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("silently guessed project identity: %d %s", rr.Code, rr.Body.String())
	}
}
