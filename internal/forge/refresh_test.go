package forge_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
)

func TestWatchReadsPreserveRateLimits(t *testing.T) {
	for _, provider := range []string{"github", "forgejo"} {
		for _, mode := range []string{"limited", "stale", "inline-limited", "inline-stale"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(mode, "inline-") && !strings.Contains(r.URL.Path, "/reviews/1/comments") {
						fmt.Fprint(w, `[{"id":1}]`)
						return
					}
					w.Header().Set("X-RateLimit-Reset", "2000000000")
					if strings.HasSuffix(mode, "limited") {
						w.WriteHeader(http.StatusTooManyRequests)
						return
					}
					w.Header().Set("X-Ocman-Cache", "stale")
					if strings.HasSuffix(r.URL.Path, "/42") || r.URL.Path == "/user" || r.URL.Path == "/api/v1/user" {
						fmt.Fprint(w, `{"number":42,"login":"me"}`)
					} else {
						fmt.Fprint(w, `[]`)
					}
				}))
				defer server.Close()
				var client forge.Forge = github.NewWithCredentials(server.URL, "token", server.Client())
				if provider == "forgejo" {
					client = forgejo.NewWithCredentials("code.example.com", server.URL, "token", server.Client())
				}
				check := func(err error) {
					t.Helper()
					var response *forgehttp.ResponseError
					if !errors.Is(err, forge.ErrRateLimited) || !errors.As(err, &response) || response.RateLimit.ResetAt.Unix() != 2000000000 {
						t.Fatalf("lost rate limit metadata: %v", err)
					}
				}
				if strings.HasPrefix(mode, "inline-") {
					if provider == "forgejo" {
						_, err := client.(forge.FeedbackReader).Feedback(t.Context(), "team/repo", 42)
						check(err)
					}
					return
				}
				ctx := forgehttp.WithFreshReads(t.Context())
				_, err := client.LookupPR(ctx, "team/repo", 42)
				check(err)
				_, err = client.CurrentUser(ctx)
				check(err)
				_, err = client.(forge.FeedbackReader).Feedback(t.Context(), "team/repo", 42)
				check(err)
				if mode == "stale" {
					if _, err := client.LookupPR(t.Context(), "team/repo", 42); err != nil {
						t.Fatal("display-only metadata should retain its stale fallback", err)
					}
					if _, err := client.CurrentUser(t.Context()); err != nil {
						t.Fatal("display-only identity should retain its stale fallback", err)
					}
				}
			})
		}
	}
}
