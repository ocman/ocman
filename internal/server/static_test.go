package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticCacheSafety(t *testing.T) {
	files := fstest.MapFS{
		"index.html":                {Data: []byte("<html>current deployment</html>")},
		"assets/index-Abcd1234.js":  {Data: []byte("console.log('hashed')")},
		"assets/stable.js":          {Data: []byte("stable")},
		"assets/page-Abcd1234.html": {Data: []byte("<html>page</html>")},
		"plugin-shared/react.js":    {Data: []byte("shared")},
		"build-hash.json":           {Data: []byte(`{"hash":"deployment"}`)},
	}
	h := staticHandler(files)
	for _, tc := range []struct{ path, cache string }{
		{"/", "no-cache"}, {"/index.html", "no-cache"}, {"/session/example", "no-cache"},
		{"/plugin-shared/react.js", "no-cache"}, {"/assets/stable.js", "no-cache"},
		{"/assets/missing-Abcd1234.js", "no-cache"},
		{"/build-hash.json", "no-cache"},
		{"/assets/page-Abcd1234.html", "no-cache"},
		{"/assets/index-Abcd1234.js", "public, max-age=31536000, immutable"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if got := w.Header().Get("Cache-Control"); got != tc.cache {
				t.Fatalf("cache = %q, want %q", got, tc.cache)
			}
		})
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := httptest.NewRequest(method, "/assets/index-Abcd1234.js", nil)
		r.Header.Set("Range", "bytes=0-6")
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusPartialContent || w.Header().Get("Content-Range") != "bytes 0-6/21" {
			t.Fatalf("range: %d %v", w.Code, w.Header())
		}
		if w.Header().Get("Content-Encoding") != "" {
			t.Fatal("standalone server must leave encoding to Caddy")
		}
		body, _ := io.ReadAll(w.Result().Body)
		if method == http.MethodHead && len(body) != 0 {
			t.Fatal("HEAD returned a body")
		}
		if method == http.MethodGet && !strings.HasPrefix(string(body), "console") {
			t.Fatalf("body = %q", body)
		}
	}
}
