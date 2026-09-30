package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
)

func TestHandleRepoFiles(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	get := func(h http.HandlerFunc, query url.Values) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil))
		return rr
	}
	q := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}

	rr := get(srv.handleRepoFiles, q("dir", dir, "remoteId", "local"))
	var list git.FileList
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &list) != nil {
		t.Fatalf("list status = %d, body = %s", rr.Code, rr.Body.String())
	}
	// .env is untracked and not ignored here, so it is listed.
	if len(list.Files) != 2 {
		t.Fatalf("files = %v", list.Files)
	}

	rr = get(srv.handleRepoFile, q("dir", dir, "remoteId", "local", "path", "foo.txt"))
	var file git.FileContent
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &file) != nil || file.Content != "hello\n" {
		t.Fatalf("file status = %d, body = %s", rr.Code, rr.Body.String())
	}

	cases := []struct {
		name  string
		h     http.HandlerFunc
		query url.Values
		want  int
	}{
		{"missing remoteId", srv.handleRepoFiles, q("dir", dir), http.StatusBadRequest},
		{"relative dir", srv.handleRepoFiles, q("dir", "rel", "remoteId", "local"), http.StatusBadRequest},
		{"unknown remote", srv.handleRepoFiles, q("dir", dir, "remoteId", "gone"), http.StatusServiceUnavailable},
		{"not a repo", srv.handleRepoFiles, q("dir", t.TempDir(), "remoteId", "local"), http.StatusNotFound},
		{"missing path", srv.handleRepoFile, q("dir", dir, "remoteId", "local"), http.StatusBadRequest},
		{"escape", srv.handleRepoFile, q("dir", dir, "remoteId", "local", "path", "../x"), http.StatusNotFound},
	}
	for _, c := range cases {
		if rr := get(c.h, c.query); rr.Code != c.want {
			t.Errorf("%s: status = %d, want %d (%s)", c.name, rr.Code, c.want, rr.Body.String())
		}
	}
}
