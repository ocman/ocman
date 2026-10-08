package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge/github"
	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

func TestProjectForgeFailureMapping(t *testing.T) {
	for _, route := range []string{"prs", "pr-checks", "issues", "forge-user"} {
		for _, tc := range []struct {
			name           string
			upstream, want int
			cancel         bool
			cancelUpstream bool
			retry          string
		}{
			{name: "cancel", want: 499, cancel: true},
			{name: "cancel during forge call", upstream: 200, want: 499, cancelUpstream: true},
			{name: "primary rate limit", upstream: 403, want: 429, retry: "17"},
			{name: "rate limit", upstream: 429, want: 429, retry: "17"},
			{name: "upstream failure", upstream: 500, want: 502},
			{name: "missing repository", upstream: 404, want: 502},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				srv := testServer(t)
				dir := initGitHubRepo(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.cancelUpstream {
						cancel()
						<-r.Context().Done()
						return
					}
					if tc.retry != "" {
						w.Header().Set("Retry-After", tc.retry)
					}
					w.WriteHeader(tc.upstream)
					_, _ = w.Write([]byte("credential-secret"))
				}))
				defer upstream.Close()
				srv.integrations.GitHub = github.NewForTest(upstream.URL, "credential-secret", upstream.Client())
				previousHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
				defer log.StandardLogger().ReplaceHooks(previousHooks)
				hook := test.NewGlobal()
				req := httptest.NewRequest(http.MethodGet, "/api/project/"+route+"?dir="+dir+"&remoteId=local&remote=origin&sha=abc123", nil)
				if tc.cancel {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				if tc.cancelUpstream {
					req = req.WithContext(ctx)
				}
				rr := httptest.NewRecorder()
				switch route {
				case "prs":
					srv.handleProjectPRs(rr, req)
				case "issues":
					srv.handleProjectIssues(rr, req)
				case "forge-user":
					srv.handleProjectForgeUser(rr, req)
				default:
					srv.handleProjectPRChecks(rr, req)
				}
				want := tc.want
				if tc.upstream == 404 && route == "pr-checks" {
					want = 200
				}
				if rr.Code != want {
					t.Fatalf("status=%d want %d body=%s", rr.Code, want, rr.Body.String())
				}
				if tc.retry != "" && rr.Header().Get("Retry-After") != tc.retry {
					t.Fatalf("Retry-After=%q", rr.Header().Get("Retry-After"))
				}
				if strings.Contains(rr.Body.String(), "credential-secret") {
					t.Fatal("upstream secret in response")
				}
				// Checks intentionally treat absent status endpoints as no CI.
				if tc.upstream == 404 && route == "pr-checks" {
					return
				}
				if tc.upstream == 404 {
					var envelope struct {
						Error struct {
							UpstreamStatus int `json:"upstreamStatus"`
						} `json:"error"`
					}
					if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil || envelope.Error.UpstreamStatus != 404 {
						t.Fatalf("404 signal lost: %s", rr.Body.String())
					}
				}
				if tc.upstream == 500 {
					entry := hook.LastEntry()
					if entry == nil || entry.Level != log.WarnLevel {
						t.Fatalf("missing warning: %v", entry)
					}
					if text, _ := entry.String(); strings.Contains(text, "credential-secret") {
						t.Fatal("upstream secret in log")
					}
					for field, want := range map[string]any{"forge_host": "github.com", "repo": "alice/myproj", "route": "/api/project/" + route, "upstream_status": 500} {
						if entry.Data[field] != want {
							t.Errorf("%s=%v want %v", field, entry.Data[field], want)
						}
					}
				}
			})
		}
	}
}
