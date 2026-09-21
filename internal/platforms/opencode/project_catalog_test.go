package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProjectCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agent":
			_, _ = w.Write([]byte(`[{"name":"build"},{"name":"plan"}]`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[{"id":"openai","models":{"gpt-5":{}}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	agents, models, err := ProjectCatalog(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0] != "build" || len(models) != 1 || models[0] != "openai/gpt-5" {
		t.Fatalf("catalog = %v, %v", agents, models)
	}
	if _, _, err := ProjectCatalog(context.Background(), "http://example.com:80"); err == nil {
		t.Fatal("expected non-loopback endpoint rejection")
	}
}
