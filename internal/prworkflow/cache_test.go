package prworkflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/github"
)

func TestListsAndWatchShareGitHubReads(t *testing.T) {
	x := newFixture(t)
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/team/repo/pulls/42":
			reads++
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "title": fmt.Sprintf("revision %d", reads), "state": "open", "mergeable": true, "updated_at": time.Now().UTC().Format(time.RFC3339), "head": map[string]string{"sha": "head"}})
		case "/user":
			fmt.Fprint(w, `{"login":"me"}`)
		case "/repos/team/repo/commits/head/status":
			fmt.Fprint(w, `{"total_count":0,"statuses":[]}`)
		case "/repos/team/repo/commits/head/check-runs":
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	client := github.NewWithCredentials(server.URL, "test-token", server.Client())
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return client, true }
	x.link(t)
	x.watch(t)
	x.request(t, "pull-requests", "", 200)
	x.request(t, "pull-requests", "", 200)
	x.m.sweep(t.Context())
	if reads != 3 {
		t.Fatalf("list and watcher did not share metadata: %d reads", reads)
	}
	x.request(t, "pull-requests?refresh=true", "", 200)
	x.request(t, "pull-requests", "", 200)
	if reads != 4 {
		t.Fatalf("explicit refresh did not revalidate exactly once: %d reads", reads)
	}
}
