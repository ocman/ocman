package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMergePRUsesViewerDefault(t *testing.T) {
	for _, method := range []string{"MERGE", "SQUASH", "REBASE", "", "UNKNOWN"} {
		t.Run(method, func(t *testing.T) {
			merges := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing authentication")
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if r.URL.Path == "/graphql" {
					variables := body["variables"].(map[string]any)
					if r.Method != http.MethodPost || variables["owner"] != "team" || variables["name"] != "repo" || !strings.Contains(body["query"].(string), "viewerDefaultMergeMethod") {
						t.Errorf("bad query: %+v", body)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]string{"viewerDefaultMergeMethod": method}}})
					return
				}
				merges++
				if r.URL.Path != "/repos/team/repo/pulls/42/merge" || r.Method != http.MethodPut || body["merge_method"] != strings.ToLower(method) || body["sha"] != "abc" || len(body) != 2 {
					t.Errorf("unexpected merge: %s %s %+v", r.Method, r.URL.Path, body)
				}
				_, _ = w.Write([]byte(`{"merged":true}`))
			}))
			defer srv.Close()
			c := &Client{apiBase: srv.URL, token: "secret", http: srv.Client()}
			err := c.MergePR(t.Context(), "team/repo", 42, "abc")
			invalid := method == "" || method == "UNKNOWN"
			if (err != nil) != invalid || (merges == 0) != invalid {
				t.Fatalf("merges=%d err=%v", merges, err)
			}
		})
	}
}

func TestMergePRErrors(t *testing.T) {
	for _, tt := range []struct {
		name, query, response    string
		queryStatus, mergeStatus int
	}{
		{"graphql refused", `{}`, ``, 403, 200},
		{"graphql error", `{"errors":[{"message":"denied"}]}`, ``, 200, 200},
		{"bad graphql", `{`, ``, 200, 200},
		{"merge refused", `{"data":{"repository":{"viewerDefaultMergeMethod":"SQUASH"}}}`, ``, 200, 409},
		{"not merged", `{"data":{"repository":{"viewerDefaultMergeMethod":"SQUASH"}}}`, `{"merged":false}`, 200, 200},
		{"bad merge response", `{"data":{"repository":{"viewerDefaultMergeMethod":"SQUASH"}}}`, `{`, 200, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/graphql" {
					w.WriteHeader(tt.queryStatus)
					_, _ = w.Write([]byte(tt.query))
					return
				}
				w.WriteHeader(tt.mergeStatus)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer srv.Close()
			c := &Client{apiBase: srv.URL, http: srv.Client()}
			if err := c.MergePR(t.Context(), "team/repo", 42, "abc"); err == nil {
				t.Fatal("expected refusal")
			}
			if err := c.MergePR(t.Context(), "bad", 42, "abc"); err == nil {
				t.Fatal("expected invalid repository")
			}
			if err := c.MergePR(t.Context(), "team/repo", 42, ""); err == nil {
				t.Fatal("expected missing head")
			}
		})
	}
}
