package prworkflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestBackgroundReadDoesNotWaitForForgeMetadata(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	before := x.f.reads
	w := x.request(t, "pull-requests?background=true", "", 200)
	if x.f.reads != before {
		t.Fatal("background counter read fetched forge metadata")
	}
	var response struct {
		PullRequests []struct {
			BackgroundTask bool `json:"backgroundTask"`
		} `json:"pullRequests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.PullRequests) != 1 || !response.PullRequests[0].BackgroundTask {
		t.Fatalf("background watch missing: %s, err=%v", w.Body, err)
	}
}

func TestBackgroundReadMatchesSidebarCounts(t *testing.T) {
	for _, tc := range []struct {
		name, status, pending string
		watching              bool
		want                  int
	}{
		{"linked", "open", "", false, 0},
		{"watching", "open", "", true, 1},
		{"queued update after watch stopped", "open", "private notice", false, 1},
		{"watch and queued update count once", "open", "private notice", true, 1},
		{"merged notice", "merged", "private notice", false, 0},
		{"closed notice", "closed", "private notice", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			l := x.only(t)
			l.Watching, l.PR.Status, l.Pending = tc.watching, tc.status, tc.pending
			if err := x.m.put(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			w := x.request(t, "pull-requests?background=true", "", 200)
			if strings.Contains(w.Body.String(), "private notice") {
				t.Fatal("background read leaked queued prompt content")
			}
			var response struct {
				PullRequests []struct {
					BackgroundTask bool `json:"backgroundTask"`
				} `json:"pullRequests"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, link := range response.PullRequests {
				if link.BackgroundTask {
					count++
				}
			}
			counts, err := BackgroundCounts(t.Context(), x.m.d.DB)
			if err != nil || count != tc.want || count != counts[state.Key{Platform: l.Platform, SessionID: l.SessionID}] {
				t.Fatalf("composer=%d sidebar=%v want=%d err=%v", count, counts, tc.want, err)
			}
		})
	}
}

func TestBackgroundReadUsesSessionOwnerAndFailsClosed(t *testing.T) {
	hub, owner, bridge := bridgeFixtures(t)
	hub.link(t)
	hub.watch(t)
	before := owner.f.reads
	w := hub.request(t, "pull-requests?background=true", "", 200)
	var response struct {
		PullRequests []Link `json:"pullRequests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.PullRequests) != 1 {
		t.Fatalf("response=%s err=%v", w.Body, err)
	}
	l := response.PullRequests[0]
	if !l.BackgroundTask || l.Platform != "r-box:opencode" || owner.f.reads != before || hub.f.reads != 0 {
		t.Fatalf("wrong owner or forge read: %+v owner reads=%d hub reads=%d", l, owner.f.reads, hub.f.reads)
	}
	bridge.err = platforms.ErrRemoteOffline
	hub.request(t, "pull-requests?background=true", "", 503)
}

func TestBackgroundReadStorageFailure(t *testing.T) {
	x := newFixture(t)
	if err := x.m.d.DB.Close(); err != nil {
		t.Fatal(err)
	}
	x.request(t, "pull-requests?background=true", "", 500)
}
