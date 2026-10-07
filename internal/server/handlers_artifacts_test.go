package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

type artifactHost struct {
	hostsvc.Host
	projects []db.ProjectStats
}

func (artifactHost) RemoteID() string { return "local" }

func (h artifactHost) Projects(context.Context) ([]db.ProjectStats, error) { return h.projects, nil }

// ProjectUpstreams folds anything under /repo (or its worktrees) to /repo.
func (artifactHost) ProjectUpstreams(_ context.Context, dir string) (*hostsvc.ProjectUpstreams, error) {
	if dir == "/repo" || strings.HasPrefix(dir, "/repo/") || strings.HasPrefix(dir, "/.worktrees/repo/") {
		return &hostsvc.ProjectUpstreams{RepoRoot: "/repo"}, nil
	}
	if dir == "/unknown" {
		return &hostsvc.ProjectUpstreams{RepoRoot: "/unknown"}, nil
	}
	return nil, errors.New("not a git repo")
}

func artifactServer(t *testing.T) (*Server, func(string, string) *httptest.ResponseRecorder) {
	t.Helper()
	srv, raw := testServerWithRawDB(t)
	srv.hostRouter = hostsvc.NewRouter(artifactHost{projects: []db.ProjectStats{{Directory: "/.worktrees/repo/feature"}}})
	for _, q := range []string{
		`INSERT INTO session (id, directory) VALUES ('top', '/repo')`,
		`INSERT INTO session (id, parent_id, directory) VALUES ('mid', 'top', '/repo')`,
		`INSERT INTO session (id, parent_id, directory) VALUES ('leaf', 'mid', '/repo')`,
		`INSERT INTO session (id, directory) VALUES ('other', '/repo')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return srv, func(method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		srv.handleArtifacts(rec, req)
		return rec
	}
}

func mustArtifact(t *testing.T, srv *Server, in ArtifactInput) state.Artifact {
	t.Helper()
	if in.Directory == "" {
		in.Directory = "/repo/sub"
	}
	a, err := srv.CreateArtifact(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCreateArtifactValidation(t *testing.T) {
	srv, _ := artifactServer(t)
	dir := t.TempDir()
	link := []ArtifactLinkInput{{URL: "https://example.com"}}
	for name, in := range map[string]ArtifactInput{
		"no title":       {Directory: "/repo", Links: link},
		"no items":       {Title: "t", Directory: "/repo"},
		"half session":   {Title: "t", Directory: "/repo", Platform: "opencode", Links: link},
		"relative dir":   {Title: "t", Directory: "repo", Links: link},
		"not a repo":     {Title: "t", Directory: "/nowhere", Links: link},
		"unknown repo":   {Title: "t", Directory: "/unknown", Links: link},
		"bad scheme":     {Title: "t", Directory: "/repo", Links: []ArtifactLinkInput{{URL: "javascript:alert(1)"}}},
		"no host":        {Title: "t", Directory: "/repo", Links: []ArtifactLinkInput{{URL: "https://"}}},
		"relative path":  {Title: "t", Directory: "/repo", Files: []ArtifactFileInput{{Path: "a.txt"}}},
		"missing path":   {Title: "t", Directory: "/repo", Files: []ArtifactFileInput{{Path: filepath.Join(dir, "missing")}}},
		"directory path": {Title: "t", Directory: "/repo", Files: []ArtifactFileInput{{Path: dir}}},
		"path + content": {Title: "t", Directory: "/repo", Files: []ArtifactFileInput{{Path: dir, Content: "x"}}},
		"no file name":   {Title: "t", Directory: "/repo", Files: []ArtifactFileInput{{Content: "x"}}},
		"slash name":     {Title: "t", Directory: "/repo", Files: []ArtifactFileInput{{Name: "../x", Content: "x"}}},
	} {
		if _, err := srv.CreateArtifact(t.Context(), in); !errors.Is(err, state.ErrArtifactInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
	if _, err := (&Server{}).CreateArtifact(t.Context(), ArtifactInput{}); err == nil {
		t.Error("created without a state database")
	}
}

func TestCreateArtifactStoresItemsAndBroadcasts(t *testing.T) {
	srv, _ := artifactServer(t)
	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := mustArtifact(t, srv, ArtifactInput{
		Title: " Report ", Directory: "/.worktrees/repo/feature", Platform: "opencode", SessionID: "top",
		Files: []ArtifactFileInput{{Path: path}, {Name: "blob", Content: "\x89PNG\r\n\x1a\n"}, {Name: "x.json", Content: "{}", MIME: "text/plain"}},
		Links: []ArtifactLinkInput{{URL: "https://example.com/pr/1", Label: "PR"}},
	})
	if a.Title != "Report" || a.Directory != "/repo" || a.RemoteID != "local" || len(a.Items) != 4 {
		t.Fatalf("artifact = %+v", a)
	}
	for i, want := range []string{"text/markdown; charset=utf-8", "image/png", "text/plain", ""} {
		if a.Items[i].MIME != want {
			t.Errorf("item %d mime = %q, want %q", i, a.Items[i].MIME, want)
		}
	}
	if a.Items[0].Name != "report.md" || a.Items[3].URL != "https://example.com/pr/1" {
		t.Fatalf("items = %+v", a.Items)
	}
	ev := <-sub.ch
	var payload map[string]string
	if ev.event != "ocman.artifact.created" || json.Unmarshal(ev.data, &payload) != nil ||
		payload["id"] != a.ID || payload["directory"] != "/repo" || payload["platform"] != "opencode" || payload["sessionId"] != "top" {
		t.Fatalf("event = %s %s", ev.event, ev.data)
	}
}

type artifactList struct {
	Artifacts  []state.Artifact `json:"artifacts"`
	NextCursor string           `json:"nextCursor"`
}

func listArtifacts(t *testing.T, do func(string, string) *httptest.ResponseRecorder, query string) artifactList {
	t.Helper()
	rec := do(http.MethodGet, "/api/artifacts"+query)
	var out artifactList
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("list %s = %d %s", query, rec.Code, rec.Body)
	}
	return out
}

func titles(l artifactList) string {
	var out []string
	for _, a := range l.Artifacts {
		out = append(out, a.Title)
	}
	// Artifacts created in the same millisecond tie-break on a random id.
	slices.Sort(out)
	return strings.Join(out, ",")
}

func TestArtifactListFiltersAndPagination(t *testing.T) {
	srv, do := artifactServer(t)
	link := []ArtifactLinkInput{{URL: "https://example.com"}}
	for _, in := range []ArtifactInput{
		{Title: "top", Platform: "opencode", SessionID: "top", Links: link},
		{Title: "leaf", Platform: "opencode", SessionID: "leaf", Links: link},
		{Title: "other", Platform: "opencode", SessionID: "other", Links: link},
		{Title: "loose", Files: []ArtifactFileInput{{Name: "needle.txt", Content: "x"}}},
	} {
		mustArtifact(t, srv, in)
	}
	for query, want := range map[string]string{
		"":                                 "leaf,loose,other,top",
		"?directory=/elsewhere":            "",
		"?directory=/repo&q=needle":        "loose",
		"?platform=opencode&sessionId=top": "top",
		"?platform=opencode&sessionId=top&includeDescendants=1": "leaf,top",
		"?platform=opencode&sessionId=mid&includeDescendants=1": "leaf",
	} {
		if got := titles(listArtifacts(t, do, query)); got != want {
			t.Errorf("list %q = %q, want %q", query, got, want)
		}
	}
	first := listArtifacts(t, do, "?limit=3")
	rest := listArtifacts(t, do, "?limit=3&cursor="+first.NextCursor)
	if len(first.Artifacts) != 3 || first.NextCursor == "" || len(rest.Artifacts) != 1 || rest.NextCursor != "" {
		t.Fatalf("pages = %+v / %+v", first, rest)
	}
	if all := titles(artifactList{Artifacts: append(first.Artifacts, rest.Artifacts...)}); all != "leaf,loose,other,top" {
		t.Fatalf("paged titles = %q", all)
	}
	loose := listArtifacts(t, do, "?q=needle").Artifacts[0]
	if loose.Items[0].URL != ArtifactFilePath(loose.ID, 0) {
		t.Fatalf("file url = %q", loose.Items[0].URL)
	}
	for _, query := range []string{"?limit=0", "?limit=x", "?cursor=bogus", "?sessionId=top"} {
		if rec := do(http.MethodGet, "/api/artifacts"+query); rec.Code != http.StatusBadRequest {
			t.Errorf("list %q = %d", query, rec.Code)
		}
	}
	if rec := do(http.MethodPost, "/api/artifacts"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	if rec := do(http.MethodGet, "/api/artifacts/a/b"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown path = %d", rec.Code)
	}
}

func TestArtifactGetStatsDelete(t *testing.T) {
	srv, do := artifactServer(t)
	a := mustArtifact(t, srv, ArtifactInput{Title: "t", Files: []ArtifactFileInput{{Name: "a.txt", Content: "hello"}}})
	rec := do(http.MethodGet, "/api/artifacts/"+a.ID)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"url":"`+ArtifactFilePath(a.ID, 0)+`"`) || !strings.Contains(rec.Body.String(), `"remoteId":"local"`) {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodGet, "/api/artifacts/stats"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"count":1,"totalBytes":5}` {
		t.Fatalf("stats = %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/artifacts/"+a.ID, nil)
	remote := httptest.NewRecorder()
	srv.handleArtifacts(remote, req)
	if remote.Code != http.StatusForbidden {
		t.Fatalf("non-local delete = %d", remote.Code)
	}
	if rec := do(http.MethodDelete, "/api/artifacts/"+a.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/artifacts/" + a.ID, "/api/artifacts/" + a.ID + "/files/0"} {
		if rec := do(http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s after delete = %d", path, rec.Code)
		}
	}
	if rec := do(http.MethodDelete, "/api/artifacts/"+a.ID); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&Server{}).handleArtifacts(rec, httptest.NewRequest(http.MethodGet, "/api/artifacts", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no state db = %d", rec.Code)
	}
}

func TestArtifactFileServing(t *testing.T) {
	srv, do := artifactServer(t)
	a := mustArtifact(t, srv, ArtifactInput{
		Title: "t",
		Files: []ArtifactFileInput{{Name: "pic.svg", Content: "<svg/>"}, {Name: "data.bin", Content: "\x00\x01"}},
		Links: []ArtifactLinkInput{{URL: "https://example.com"}},
	})
	rec := do(http.MethodGet, ArtifactFilePath(a.ID, 0))
	h := rec.Header()
	if rec.Code != http.StatusOK || rec.Body.String() != "<svg/>" || h.Get("Content-Type") != "image/svg+xml" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox; frame-ancestors 'self'" || h.Get("X-Frame-Options") != "SAMEORIGIN" ||
		h.Get("Content-Disposition") != "inline; filename=pic.svg" {
		t.Fatalf("inline = %d %v %q", rec.Code, h, rec.Body)
	}
	if rec := do(http.MethodGet, ArtifactFilePath(a.ID, 0)+"?download=1"); rec.Header().Get("Content-Disposition") != "attachment; filename=pic.svg" {
		t.Fatalf("download disposition = %q", rec.Header().Get("Content-Disposition"))
	}
	if rec := do(http.MethodGet, ArtifactFilePath(a.ID, 1)); rec.Header().Get("Content-Disposition") != "attachment; filename=data.bin" {
		t.Fatalf("binary disposition = %q", rec.Header().Get("Content-Disposition"))
	}
	for _, ord := range []string{"2", "3", "-1", "x"} {
		if rec := do(http.MethodGet, "/api/artifacts/"+a.ID+"/files/"+ord); rec.Code != http.StatusNotFound {
			t.Errorf("ordinal %s = %d", ord, rec.Code)
		}
	}
}

func TestArtifactInteractivePreview(t *testing.T) {
	srv, do := artifactServer(t)
	page := "<!doctype html><script>document.body.textContent='js'</script>"
	a := mustArtifact(t, srv, ArtifactInput{Title: "t", Files: []ArtifactFileInput{{Name: "board.html", Content: page}, {Name: "pic.svg", Content: "<svg/>"}}})
	if !strings.HasPrefix(a.Items[0].MIME, "text/html") {
		t.Fatalf("mime = %q", a.Items[0].MIME)
	}

	for _, path := range []string{ArtifactFilePath(a.ID, 0) + "/interactive", ArtifactFilePath(a.ID, 0) + "/interactive?page=job&download=1"} {
		rec := do(http.MethodGet, path)
		h := rec.Header()
		if rec.Code != http.StatusOK || rec.Body.String() != page || h.Get("Content-Security-Policy") != interactivePreviewCSP ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("Content-Disposition") != "inline; filename=board.html" {
			t.Fatalf("GET %s = %d %v %q", path, rec.Code, h, rec.Body)
		}
	}
	// The plain file and its download stay inert, and the bytes are untouched.
	for path, disposition := range map[string]string{ArtifactFilePath(a.ID, 0): "inline", ArtifactFilePath(a.ID, 0) + "?download=1": "attachment"} {
		rec := do(http.MethodGet, path)
		if rec.Header().Get("Content-Security-Policy") != inertFileCSP || rec.Body.String() != page || rec.Header().Get("Content-Disposition") != disposition+"; filename=board.html" {
			t.Fatalf("GET %s = %v %q", path, rec.Header(), rec.Body)
		}
	}
	for _, path := range []string{ArtifactFilePath(a.ID, 1) + "/interactive", ArtifactFilePath(a.ID, 9) + "/interactive", ArtifactFilePath(a.ID, 0) + "/other"} {
		if rec := do(http.MethodGet, path); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Security-Policy") == interactivePreviewCSP {
			t.Errorf("GET %s = %d %v", path, rec.Code, rec.Header())
		}
	}
	if rec := do(http.MethodPost, ArtifactFilePath(a.ID, 0)+"/interactive"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST interactive = %d", rec.Code)
	}
}

// The Playwright suite replays these exact headers; keep them in step.
func TestArtifactPreviewHeadersMatchE2E(t *testing.T) {
	raw, err := os.ReadFile("../../frontend/e2e/artifact-html/headers.json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Inert, Interactive string }
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Inert != inertFileCSP || got.Interactive != interactivePreviewCSP {
		t.Fatalf("frontend/e2e/artifact-html/headers.json = %+v, want inert %q interactive %q", got, inertFileCSP, interactivePreviewCSP)
	}
}
