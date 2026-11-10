package ocapi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestIsolatedTransportsOwnDialectDiscovery(t *testing.T) {
	var mu sync.Mutex
	probes := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, _ := r.BasicAuth()
		if r.URL.Path == "/api/info" {
			mu.Lock()
			probes[password]++
			mu.Unlock()
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	for _, password := range []string{"alpha", "beta"} {
		client := &http.Client{Transport: New(password).IsolatedTransport(http.DefaultTransport)}
		for range 2 {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/config", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if probes["alpha"] != 1 || probes["beta"] != 1 {
		t.Fatalf("dialect cache shared across account auth: %v", probes)
	}
}
