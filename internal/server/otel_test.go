package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteTemplate(t *testing.T) {
	cases := map[string]string{
		"/api/sessions":                          "/api/sessions",
		"/api/session/abc-123":                   "/api/session/{id}",
		"/api/session/abc-123/info":              "/api/session/{id}/info",
		"/api/session/abc-123/permissions":       "/api/session/{id}/permissions",
		"/api/session/abc-123/permissions/pid42": "/api/session/{id}/permissions",
		"/api/session/abc-123/events":            "/api/session/{id}/events",
		"/api/session/archive":                   "/api/session/archive",
		"/api/session/seen":                      "/api/session/seen",
		"/api/stats":                             "/api/stats",
		"/":                                      "/",
	}
	for in, want := range cases {
		if got := routeTemplate(in); got != want {
			t.Errorf("routeTemplate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOtelSpanFilter(t *testing.T) {
	cases := map[string]bool{
		"/api/stats":              true,
		"/api/session/abc/info":   true,
		"/api/session/abc/events": false, // SSE skipped
		"/":                       false, // static skipped
		"/index.html":             false,
		"/assets/x.js":            false,
	}
	for path, want := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if got := otelSpanFilter(req); got != want {
			t.Errorf("otelSpanFilter(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestOtelSpanName(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/session/abc/info", nil)
	got := otelSpanName("ignored", req)
	want := "GET /api/session/{id}/info"
	if got != want {
		t.Errorf("otelSpanName = %q, want %q", got, want)
	}
}
