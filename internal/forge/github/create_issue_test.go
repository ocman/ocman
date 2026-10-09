package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssue(t *testing.T) {
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Method != http.MethodPost || r.URL.Path != "/repos/alice/repo/issues" || r.Header.Get("Authorization") != "Bearer token" || json.NewDecoder(r.Body).Decode(&body) != nil || body["title"] != "Gap" || body["body"] != "Details" {
			t.Errorf("request = %s %s %v", r.Method, r.URL.Path, body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"number":12,"title":"Gap","state":"open","html_url":"https://github.com/alice/repo/issues/12"}`))
	}))
	defer srv.Close()
	issue, err := newTestClient(t, srv, "token").CreateIssue(t.Context(), "alice/repo", "Gap", "Details")
	if err != nil || issue.Number != 12 || issue.URL != "https://github.com/alice/repo/issues/12" {
		t.Fatalf("issue = %#v, %v", issue, err)
	}
	status = http.StatusForbidden
	if _, err := newTestClient(t, srv, "token").CreateIssue(t.Context(), "alice/repo", "Gap", "Details"); err == nil {
		t.Fatal("forbidden create succeeded")
	}
}
