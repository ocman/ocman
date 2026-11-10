package autoapprove

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestReviewerFileConfigurationRefusesExternalSecretReplacement(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("REVIEWER_CONFIG_TEST", "external-reviewer-key")
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := "version: 1\nsettings:\n  judge_endpoint:\n    format: openai\n    endpoint: https://reviewer.example/v1/chat/completions\n    model: reviewer\n    minSafeProbability: 0.99\n    apiKey: secret://env/REVIEWER_CONFIG_TEST\n"
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	m := &Module{ModuleDeps: ModuleDeps{Store: store}}
	get := httptest.NewRecorder()
	m.handleJudgeEndpoint(get, httptest.NewRequest(http.MethodGet, "/", nil))
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), "external-reviewer-key") || !strings.Contains(get.Body.String(), `"apiKeySet":true`) {
		t.Fatalf("unsafe reviewer response: %d %s", get.Code, get.Body.String())
	}
	for _, key := range []string{`"replacement"`, `""`} {
		body := `{"format":"openai","endpoint":"https://reviewer.example/v1/chat/completions","model":"reviewer","minSafeProbability":0.99,"apiKey":` + key + `}`
		post := httptest.NewRecorder()
		m.handleJudgeEndpoint(post, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if post.Code != http.StatusConflict || !strings.Contains(post.Body.String(), "externally managed") {
			t.Fatalf("external update: %d %s", post.Code, post.Body.String())
		}
	}
	// A non-secret edit keeps the original external reference.
	body := `{"format":"openai","endpoint":"https://reviewer.example/v1/chat/completions","model":"new","minSafeProbability":0.99}`
	post := httptest.NewRecorder()
	m.handleJudgeEndpoint(post, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if post.Code != http.StatusOK {
		t.Fatalf("non-secret update: %d %s", post.Code, post.Body.String())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "external-reviewer-key") || !strings.Contains(string(data), "secret://env/REVIEWER_CONFIG_TEST") {
		t.Fatal("reviewer save copied or lost external credential")
	}
}
