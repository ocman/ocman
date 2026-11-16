package forgejo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergePRDefault(t *testing.T) {
	for _, style := range []string{"merge", "squash", "rebase", "rebase-merge", "fast-forward-only", "", "manually-merged", "unknown"} {
		t.Run(style, func(t *testing.T) {
			merges := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "token secret" {
					t.Error("missing authentication")
				}
				switch r.URL.Path {
				case "/api/v1/repos/team/repo":
					_ = json.NewEncoder(w).Encode(map[string]string{"default_merge_style": style})
				case "/api/v1/repos/team/repo/pulls/42/merge":
					merges++
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if r.Method != http.MethodPost || body["Do"] != style || body["head_commit_id"] != "abc" || len(body) != 2 {
						t.Errorf("unexpected merge: %s %+v", r.Method, body)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, token: "secret", http: srv.Client()}
			err := c.MergePR(t.Context(), "team/repo", 42, "abc")
			invalid := style == "" || style == "unknown" || style == "manually-merged"
			if (err != nil) != invalid || (merges == 0) != invalid {
				t.Fatalf("merges=%d err=%v", merges, err)
			}
		})
	}
}

func TestMergePRErrors(t *testing.T) {
	for _, tt := range []struct {
		name, settings         string
		getStatus, mergeStatus int
		sha                    string
	}{
		{"settings refused", `{}`, 403, 200, "abc"},
		{"bad settings", `{`, 200, 200, "abc"},
		{"missing head", `{"default_merge_style":"squash"}`, 200, 200, ""},
		{"merge refused", `{"default_merge_style":"squash"}`, 200, 409, "abc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(tt.getStatus)
					_, _ = w.Write([]byte(tt.settings))
					return
				}
				w.WriteHeader(tt.mergeStatus)
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, http: srv.Client()}
			if err := c.MergePR(t.Context(), "team/repo", 42, tt.sha); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}
