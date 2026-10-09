package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
)

func TestJudgeEndpointSettings(t *testing.T) {
	sdb := openTestStateDB(t)
	srv := &Server{stateDB: sdb}
	call := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleJudgeEndpoint(rec, httptest.NewRequest(method, "/api/settings/judge-endpoint", strings.NewReader(body)))
		return rec
	}
	get := call(http.MethodGet, "")
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"minSafeProbability":0.99`) {
		t.Fatalf("default: %d %s", get.Code, get.Body)
	}
	post := call(http.MethodPost, `{"format":"typesafe","endpoint":"http://localhost:8080/v1/systemone","model":"local","apiKey":"secret-test-key","minSafeProbability":0.99}`)
	if post.Code != 200 || strings.Contains(post.Body.String(), "secret-test-key") || !strings.Contains(post.Body.String(), `"apiKeySet":true`) {
		t.Fatalf("save: %d %s", post.Code, post.Body)
	}
	get = call(http.MethodGet, "")
	if strings.Contains(get.Body.String(), "secret-test-key") || get.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("key leaked: %s", get.Body)
	}
	post = call(http.MethodPost, `{"format":"typesafe","endpoint":"http://localhost:8080/v1/systemone","minSafeProbability":0.98}`)
	config, err := autoapprove.LoadJudgeEndpoint(t.Context(), sdb)
	if post.Code != 200 || err != nil || config.APIKey != "secret-test-key" {
		t.Fatalf("key not preserved: %d %+v %v", post.Code, config, err)
	}
	post = call(http.MethodPost, `{"format":"","endpoint":"","apiKey":"","minSafeProbability":0.99}`)
	config, err = autoapprove.LoadJudgeEndpoint(t.Context(), sdb)
	if post.Code != 200 || err != nil || config.APIKey != "" || config.Format != "" {
		t.Fatalf("clear: %d %+v %v", post.Code, config, err)
	}
	for _, body := range []string{`{`, `{"format":"unknown"}`, `{"format":"openai","endpoint":"file:///tmp/model","model":"local","minSafeProbability":0.99}`} {
		if rec := call(http.MethodPost, body); rec.Code != 400 {
			t.Errorf("invalid body accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := call(http.MethodDelete, ""); rec.Code != 405 {
		t.Fatalf("method: %d", rec.Code)
	}
}

func TestJudgeEndpointNoDatabase(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).handleJudgeEndpoint(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 503 {
		t.Fatalf("status: %d", rec.Code)
	}
}
