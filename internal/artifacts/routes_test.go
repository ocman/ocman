package artifacts

import (
	"net/http"
	"testing"
)

func TestArtifactRouteMethods(t *testing.T) {
	_, request := artifactServer(t)
	for _, tc := range []struct{ method, path, allow string }{
		{"POST", "/api/artifacts", "GET, HEAD"},
		{"POST", "/api/artifacts/", "GET, HEAD"},
		{"POST", "/api/artifacts/stats", "GET, HEAD"},
		{"DELETE", "/api/artifacts/stats", "GET, HEAD"},
		{"POST", "/api/artifacts/example", "DELETE, GET, HEAD"},
		{"GET", "/api/artifacts/example/share", "POST"},
		{"POST", "/api/artifacts/example/shares", "GET, HEAD"},
		{"GET", "/api/artifacts/example/share/token", "DELETE"},
		{"POST", "/api/artifacts/example/files/0", "GET, HEAD"},
		{"POST", "/api/artifacts/example/files/0/interactive", "GET, HEAD"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := request(tc.method, tc.path)
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != tc.allow {
				t.Fatalf("status=%d, Allow=%q; want 405, %q", rec.Code, rec.Header().Get("Allow"), tc.allow)
			}
		})
	}
	for _, path := range []string{"/api/artifacts/example/unknown", "/api/artifacts/example/files/0/unknown"} {
		if rec := request(http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d; want 404", path, rec.Code)
		}
	}
}
