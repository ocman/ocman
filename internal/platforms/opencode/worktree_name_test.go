package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWorktreeName(t *testing.T) {
	for _, tc := range []struct {
		name, config, answer, want string
		failPrompt                 bool
	}{
		{"configured", `{"small_model":"test/fast"}`, `{"parts":[{"type":"reasoning","text":"ignored"},{"type":"text","text":"Fix Login Flow"}]}`, "fix-login-flow", false},
		{"default", `{}`, `{"parts":[{"type":"text","text":"../unsafe; rm -rf /"}]}`, "unsafe-rm-rf", false},
		{"long", `{}`, `{"parts":[{"type":"text","text":"` + strings.Repeat("a", 80) + `"}]}`, strings.Repeat("a", 48), false},
		{"empty", `{}`, `{"parts":[{"type":"text","text":"!!!"}]}`, "", false},
		{"bad-config", `{"small_model":"invalid"}`, `{}`, "", false},
		{"bad-json", `{`, `{}`, "", false},
		{"bad-answer", `{}`, `{`, "", false},
		{"upstream-error", `{}`, `{}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created, removed := false, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("directory") != "/repo" {
					t.Errorf("missing owner directory: %s", r.URL)
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /config":
					_, _ = w.Write([]byte(tc.config))
				case "POST /session":
					var body struct {
						Permission []struct{ Permission, Pattern, Action string } `json:"permission"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body.Permission) != 1 || body.Permission[0].Permission != "*" || body.Permission[0].Action != "deny" {
						t.Errorf("unsafe naming permissions: %+v", body)
					}
					created = true
					_, _ = w.Write([]byte(`{"id":"naming"}`))
				case "POST /session/naming/message":
					var body struct {
						Model struct{ ProviderID, ModelID string }
						Agent string
						Parts []struct{ Text string }
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					wantModel := "anthropic/claude-haiku-4-5"
					if tc.name == "configured" {
						wantModel = "test/fast"
					}
					if body.Model.ProviderID+"/"+body.Model.ModelID != wantModel || body.Agent != "title" {
						t.Errorf("wrong model/agent: %+v", body)
					}
					if len(body.Parts) != 1 || len(body.Parts[0].Text) > 2300 {
						t.Errorf("unbounded naming prompt")
					}
					if tc.failPrompt {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(tc.answer))
				case "POST /session/naming/abort":
					w.WriteHeader(http.StatusNoContent)
				case "DELETE /session/naming":
					removed = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			got, err := WorktreeName(context.Background(), u.Port(), "/repo", strings.Repeat("x", 3000))
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			if created && !removed {
				t.Fatal("temporary naming session leaked")
			}
		})
	}
}
