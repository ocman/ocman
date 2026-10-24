package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge/github"
)

func TestProjectForgeReadsRefreshOnlyWithinBudget(t *testing.T) {
	for _, endpoint := range []string{"prs", "issues", "pr-checks", "pr-mergeability", "forge-user"} {
		t.Run(endpoint, func(t *testing.T) {
			s := testServer(t)
			dir := initGitHubRepo(t)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-RateLimit-Limit", "8")
				w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(8-calls))
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
				switch r.URL.Path {
				case "/user":
					fmt.Fprint(w, `{"login":"alice"}`)
				case "/repos/alice/myproj/pulls/42":
					fmt.Fprint(w, `{"number":42,"mergeable":true}`)
				default:
					if endpoint == "pr-checks" {
						fmt.Fprint(w, `{"total_count":0,"statuses":[],"check_runs":[]}`)
					} else {
						fmt.Fprint(w, `[]`)
					}
				}
			}))
			defer upstream.Close()
			s.integrations.GitHub = github.NewWithCredentials(upstream.URL, "token", upstream.Client())
			handler := map[string]http.HandlerFunc{"prs": projectForgeRoute(s), "issues": projectForgeRoute(s),
				"pr-checks": projectForgeRoute(s), "pr-mergeability": projectForgeRoute(s), "forge-user": projectForgeRoute(s)}[endpoint]
			request := func(refresh bool) {
				wantStatus := http.StatusOK
				if endpoint == "pr-mergeability" && calls >= 4 {
					// Stale reviews must be retryable, never a fresh approval.
					wantStatus = http.StatusTooManyRequests
				}
				url := "/api/project/" + endpoint + "?dir=" + dir + "&remoteId=local&remote=origin&sha=abc123&number=42"
				if refresh {
					url += "&refresh=1"
				}
				rr := httptest.NewRecorder()
				handler(rr, httptest.NewRequest(http.MethodGet, url, nil))
				if rr.Code != wantStatus {
					t.Fatalf("%s: status=%d body=%s", endpoint, rr.Code, rr.Body.String())
				}
			}
			request(false)
			first := calls
			request(false)
			if calls != first {
				t.Fatalf("normal read refetched: %d -> %d", first, calls)
			}
			request(true)
			if calls != first*2 {
				t.Fatalf("explicit refresh did not revalidate: %d", calls)
			}
			for range 4 {
				request(true)
			}
			if calls != 4 {
				t.Fatalf("refresh exceeded half the quota: %d", calls)
			}
		})
	}
}
