package artifacts

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
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type broadcast struct{ event, data string }

// relayURL is read through Deps.RelayURL, so a test can configure the relay
// after the module is built, as main.go's options can.
var relayURL string

// artifactServer is the module over a temporary store, with /repo (and its
// worktrees) as the only project and a top > mid > leaf session tree.
func artifactServer(t *testing.T) (*Module, func(string, string) *httptest.ResponseRecorder) {
	t.Helper()
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); relayURL = "" })
	tree := map[string][]string{"top": {"top", "mid", "leaf"}, "mid": {"mid", "leaf"}}
	localOnly := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	pass := func(h http.HandlerFunc) http.HandlerFunc { return h }
	srv := NewModule(Deps{
		Store: store,
		ResolveProject: func(_ context.Context, dir string) (string, error) {
			if dir == "/repo" || strings.HasPrefix(dir, "/repo/") || strings.HasPrefix(dir, "/.worktrees/repo/") {
				return "/repo", nil
			}
			if dir == "/unknown" {
				return "/unknown", nil
			}
			return "", errors.New("not a git repo")
		},
		Projects: func(context.Context) ([]db.ProjectStats, error) {
			return []db.ProjectStats{{Directory: "/.worktrees/repo/feature"}}, nil
		},
		Descendants: func(_ context.Context, id string) ([]string, error) {
			if ids, ok := tree[id]; ok {
				return ids, nil
			}
			return []string{id}, nil
		},
		Guards:      module.Guards{Auth: pass, Localhost: localOnly},
		FileAuth:    pass,
		SignFileURL: func(path string) string { return "https://ocman.example" + path },
		PublicURL:   func(path string) string { return "https://ocman.example" + path },
		RelayURL:    func() string { return relayURL },
		Broadcast:   func(string, []byte) {},
	})
	return srv, func(method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
}

func mustArtifact(t *testing.T, srv *Module, in Input) state.Artifact {
	t.Helper()
	if in.Directory == "" {
		in.Directory = "/repo/sub"
	}
	a, err := srv.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCreateArtifactValidation(t *testing.T) {
	srv, _ := artifactServer(t)
	dir := t.TempDir()
	link := []LinkInput{{URL: "https://example.com"}}
	for name, in := range map[string]Input{
		"no title":       {Directory: "/repo", Links: link},
		"no items":       {Title: "t", Directory: "/repo"},
		"half session":   {Title: "t", Directory: "/repo", Platform: "opencode", Links: link},
		"relative dir":   {Title: "t", Directory: "repo", Links: link},
		"not a repo":     {Title: "t", Directory: "/nowhere", Links: link},
		"unknown repo":   {Title: "t", Directory: "/unknown", Links: link},
		"bad scheme":     {Title: "t", Directory: "/repo", Links: []LinkInput{{URL: "javascript:alert(1)"}}},
		"no host":        {Title: "t", Directory: "/repo", Links: []LinkInput{{URL: "https://"}}},
		"relative path":  {Title: "t", Directory: "/repo", Files: []FileInput{{Path: "a.txt"}}},
		"missing path":   {Title: "t", Directory: "/repo", Files: []FileInput{{Path: filepath.Join(dir, "missing")}}},
		"directory path": {Title: "t", Directory: "/repo", Files: []FileInput{{Path: dir}}},
		"path + content": {Title: "t", Directory: "/repo", Files: []FileInput{{Path: dir, Content: "x"}}},
		"no file name":   {Title: "t", Directory: "/repo", Files: []FileInput{{Content: "x"}}},
		"slash name":     {Title: "t", Directory: "/repo", Files: []FileInput{{Name: "../x", Content: "x"}}},
	} {
		if _, err := srv.Create(t.Context(), in); !errors.Is(err, state.ErrArtifactInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
}

func TestCreateArtifactStoresItemsAndBroadcasts(t *testing.T) {
	srv, _ := artifactServer(t)
	events := make(chan broadcast, 1)
	srv.Broadcast = func(event string, data []byte) { events <- broadcast{event, string(data)} }
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := mustArtifact(t, srv, Input{
		Title: " Report ", Directory: "/.worktrees/repo/feature", Platform: "opencode", SessionID: "top",
		Files: []FileInput{{Path: path}, {Name: "blob", Content: "\x89PNG\r\n\x1a\n"}, {Name: "x.json", Content: "{}", MIME: "text/plain"}},
		Links: []LinkInput{{URL: "https://example.com/pr/1", Label: "PR"}},
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
	ev := <-events
	var payload map[string]string
	if ev.event != "ocman.artifact.created" || json.Unmarshal([]byte(ev.data), &payload) != nil ||
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
	link := []LinkInput{{URL: "https://example.com"}}
	for _, in := range []Input{
		{Title: "top", Platform: "opencode", SessionID: "top", Links: link},
		{Title: "leaf", Platform: "opencode", SessionID: "leaf", Links: link},
		{Title: "other", Platform: "opencode", SessionID: "other", Links: link},
		{Title: "loose", Files: []FileInput{{Name: "needle.txt", Content: "x"}}},
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
	if loose.Items[0].URL != FilePath(loose.ID, 0) {
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
	a := mustArtifact(t, srv, Input{Title: "t", Files: []FileInput{{Name: "a.txt", Content: "hello"}}})
	rec := do(http.MethodGet, "/api/artifacts/"+a.ID)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"url":"`+FilePath(a.ID, 0)+`"`) || !strings.Contains(rec.Body.String(), `"remoteId":"local"`) {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodGet, "/api/artifacts/stats"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"count":1,"totalBytes":5}` {
		t.Fatalf("stats = %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/artifacts/"+a.ID, nil)
	remote := httptest.NewRecorder()
	srv.ServeHTTP(remote, req)
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
}

func TestArtifactFileServing(t *testing.T) {
	srv, do := artifactServer(t)
	a := mustArtifact(t, srv, Input{
		Title: "t",
		Files: []FileInput{{Name: "pic.svg", Content: "<svg/>"}, {Name: "data.bin", Content: "\x00\x01"}},
		Links: []LinkInput{{URL: "https://example.com"}},
	})
	rec := do(http.MethodGet, FilePath(a.ID, 0))
	h := rec.Header()
	if rec.Code != http.StatusOK || rec.Body.String() != "<svg/>" || h.Get("Content-Type") != "image/svg+xml" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox; frame-ancestors 'self'" || h.Get("X-Frame-Options") != "SAMEORIGIN" ||
		h.Get("Content-Disposition") != "inline; filename=pic.svg" {
		t.Fatalf("inline = %d %v %q", rec.Code, h, rec.Body)
	}
	if rec := do(http.MethodGet, FilePath(a.ID, 0)+"?download=1"); rec.Header().Get("Content-Disposition") != "attachment; filename=pic.svg" {
		t.Fatalf("download disposition = %q", rec.Header().Get("Content-Disposition"))
	}
	if rec := do(http.MethodGet, FilePath(a.ID, 1)); rec.Header().Get("Content-Disposition") != "attachment; filename=data.bin" {
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
	a := mustArtifact(t, srv, Input{Title: "t", Files: []FileInput{{Name: "board.html", Content: page}, {Name: "pic.svg", Content: "<svg/>"}}})
	if !strings.HasPrefix(a.Items[0].MIME, "text/html") {
		t.Fatalf("mime = %q", a.Items[0].MIME)
	}

	for _, path := range []string{FilePath(a.ID, 0) + "/interactive", FilePath(a.ID, 0) + "/interactive?page=job&download=1"} {
		rec := do(http.MethodGet, path)
		h := rec.Header()
		if rec.Code != http.StatusOK || rec.Body.String() != page || h.Get("Content-Security-Policy") != InteractivePreviewCSP ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("Content-Disposition") != "inline; filename=board.html" {
			t.Fatalf("GET %s = %d %v %q", path, rec.Code, h, rec.Body)
		}
	}
	// The plain file and its download stay inert, and the bytes are untouched.
	for path, disposition := range map[string]string{FilePath(a.ID, 0): "inline", FilePath(a.ID, 0) + "?download=1": "attachment"} {
		rec := do(http.MethodGet, path)
		if rec.Header().Get("Content-Security-Policy") != httpx.InertFileCSP || rec.Body.String() != page || rec.Header().Get("Content-Disposition") != disposition+"; filename=board.html" {
			t.Fatalf("GET %s = %v %q", path, rec.Header(), rec.Body)
		}
	}
	for _, path := range []string{FilePath(a.ID, 1) + "/interactive", FilePath(a.ID, 9) + "/interactive", FilePath(a.ID, 0) + "/other"} {
		if rec := do(http.MethodGet, path); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Security-Policy") == InteractivePreviewCSP {
			t.Errorf("GET %s = %d %v", path, rec.Code, rec.Header())
		}
	}
	if rec := do(http.MethodPost, FilePath(a.ID, 0)+"/interactive"); rec.Code != http.StatusMethodNotAllowed {
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
	if got.Inert != httpx.InertFileCSP || got.Interactive != InteractivePreviewCSP {
		t.Fatalf("frontend/e2e/artifact-html/headers.json = %+v, want inert %q interactive %q", got, httpx.InertFileCSP, InteractivePreviewCSP)
	}
}
