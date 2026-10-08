package ocruntime

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbePreservesTemporaryHTTPFailures(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			runtime := &NativeRuntime{httpClient: server.Client()}
			err := runtime.Probe(t.Context(), &Instance{Endpoint: server.URL})
			if !errors.Is(err, ErrProbeNotReady) || !errors.Is(err, ErrProbeUnreachable) {
				t.Fatalf("temporary HTTP failure lost classification: %v", err)
			}
		})
	}
}
