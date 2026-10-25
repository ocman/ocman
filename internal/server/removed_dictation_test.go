package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDictationRoutesRemoved(t *testing.T) {
	srv := testServer(t)
	for _, tt := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/whisper/status"},
		{http.MethodPost, "/api/transcribe"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			serveRoutes(t, srv, rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != http.StatusNotFound {
				t.Fatalf("%s %s: got %d, want 404", tt.method, tt.path, rr.Code)
			}
		})
	}
}
