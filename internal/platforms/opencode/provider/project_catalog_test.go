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
			// 302ai sorts before every real provider; listing it would push
			// connected providers past the picker's render cap.
			_, _ = w.Write([]byte(`{"all":[{"id":"302ai","models":{"claude-opus-5-5":{}}},{"id":"openai","models":{"gpt-5":{}}}],"connected":["openai"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	agents, models, err := ProjectCatalog(context.Background(), server.URL, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0] != "build" || len(models) != 1 || models[0] != "openai/gpt-5" {
		t.Fatalf("catalog = %v, %v", agents, models)
	}
	if _, _, err := ProjectCatalog(context.Background(), "http://example.com:80", ""); err == nil {
		t.Fatal("expected non-loopback endpoint rejection")
	}
}
