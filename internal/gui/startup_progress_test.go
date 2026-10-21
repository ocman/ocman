package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDesktopOpensStartupScreenBeforeApplicationIsReady(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/startup" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"starting","phase":"Migrating"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	w := httptest.NewRecorder()
	newBootstrapHandler(backend.URL, time.Second).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "location.replace") {
		t.Fatalf("desktop waited for migrations instead of showing progress: %d %s", w.Code, w.Body.String())
	}
}

func TestDesktopStillSupportsStandaloneServers(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/startup" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()
	if err := waitForServer(backend.URL, time.Second); err != nil {
		t.Fatal(err)
	}
}
