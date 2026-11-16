package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
	"github.com/ocman/ocman/internal/platforms"
)

type mergeForge struct {
	*fakeForge
	merged int
	err    error
	stale  bool
}

func (f *mergeForge) Checks(ctx context.Context, repo, sha string) (forge.CIStatus, forge.RateLimit, error) {
	ci, rl, err := f.fakeForge.Checks(ctx, repo, sha)
	rl.Stale = f.stale
	return ci, rl, err
}

func (f *mergeForge) MergePR(ctx context.Context, repo string, number int, sha string) error {
	if !forgehttp.FreshReads(ctx) || repo != "team/repo" || number != 42 || sha != "abc" {
		panic("merge must use fresh reads and the linked PR's expected head")
	}
	f.merged++
	return f.err
}

const mergeBody = `{"action":"merge","host":"github.com","repo":"team/repo","number":42,"headSha":"abc"}`

func readyMerge(t *testing.T) (*fixture, *mergeForge) {
	t.Helper()
	x := newFixture(t)
	x.f.pr.Mergeable = new(bool)
	*x.f.pr.Mergeable = true
	x.f.checks = forge.CIStatus{State: forge.CIStateSuccess, Checks: []forge.Check{{Name: "tests", State: forge.CIStateSuccess}}}
	x.link(t)
	f := &mergeForge{fakeForge: x.f}
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return f, true }
	return x, f
}

func TestMergeLinkedPR(t *testing.T) {
	x, f := readyMerge(t)
	x.watch(t)
	x.request(t, "pull-requests", mergeBody, http.StatusOK)
	l := x.only(t)
	if f.merged != 1 || l.PR.Status != "merged" || l.Watching || l.hasBackgroundTask() {
		t.Fatalf("merge=%d link=%+v", f.merged, l)
	}
	// A repeated request must re-read the forge and refuse the closed PR.
	x.f.pr.Status = "merged"
	x.request(t, "pull-requests", mergeBody, http.StatusConflict)
	if f.merged != 1 {
		t.Fatal("merged twice")
	}
}

func TestMergeRefusesUnsafePRs(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*fixture, *mergeForge)
		status int
	}{
		{"changed head", func(x *fixture, _ *mergeForge) { x.f.pr.HeadSHA = "def" }, 409},
		{"draft", func(x *fixture, _ *mergeForge) { x.f.pr.Status = "draft" }, 409},
		{"closed", func(x *fixture, _ *mergeForge) { x.f.pr.Status = "closed" }, 409},
		{"conflict", func(x *fixture, _ *mergeForge) { *x.f.pr.Mergeable = false }, 409},
		{"unknown mergeability", func(x *fixture, _ *mergeForge) { x.f.pr.Mergeable = nil }, 409},
		{"pending", func(x *fixture, _ *mergeForge) { x.f.checks.State = forge.CIStatePending }, 409},
		{"failed", func(x *fixture, _ *mergeForge) { x.f.checks.Checks[0].State = forge.CIStateFailure }, 409},
		{"no checks", func(x *fixture, _ *mergeForge) { x.f.checks.Checks = nil }, 409},
		{"check error", func(x *fixture, _ *mergeForge) { x.f.checkErr = errors.New("offline") }, 502},
		{"rate limited", func(x *fixture, _ *mergeForge) { x.f.limited = true }, 502},
		{"stale checks", func(_ *fixture, f *mergeForge) { f.stale = true }, 502},
		{"lookup error", func(x *fixture, _ *mergeForge) { x.f.err = errors.New("offline") }, 502},
		{"owner offline", func(x *fixture, _ *mergeForge) { x.ownerErr = errors.New("offline") }, 503},
		{"forge missing", func(x *fixture, _ *mergeForge) {
			x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return nil, false }
		}, 503},
		{"unsupported", func(x *fixture, _ *mergeForge) {
			x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return x.f, true }
		}, 501},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x, f := readyMerge(t)
			tt.change(x, f)
			x.request(t, "pull-requests", mergeBody, tt.status)
			if f.merged != 0 || x.only(t).PR.Status != "open" {
				t.Fatal("unsafe merge changed the PR")
			}
		})
	}
}

func TestMergeRefusalPreservesWatch(t *testing.T) {
	x, f := readyMerge(t)
	x.watch(t)
	f.err = errors.New("branch protection")
	x.request(t, "pull-requests", mergeBody, 502)
	if !x.only(t).Watching || x.only(t).PR.Status != "open" {
		t.Fatal("forge refusal cleared the watch")
	}
}

func TestMergeRequiresLinkedPRAndHead(t *testing.T) {
	x, f := readyMerge(t)
	x.request(t, "pull-requests", `{"action":"merge","host":"github.com","repo":"other/repo","number":42,"headSha":"abc"}`, 404)
	x.request(t, "pull-requests", `{"action":"merge","host":"github.com","repo":"team/repo","number":42}`, 409)
	if f.merged != 0 {
		t.Fatal("merged without a linked PR and expected head")
	}
}

func TestMergeOwnerOperation(t *testing.T) {
	x, f := readyMerge(t)
	response := x.m.Operation(t.Context(), "r-box:opencode", platforms.PullRequestRequest{Operation: "change", SessionID: "s1", Body: []byte(mergeBody)})
	if response.Status != 200 || f.merged != 1 {
		t.Fatalf("owner merge: %+v, merges=%d", response, f.merged)
	}
}

func TestMergeForwardsToOwnerAndFailsClosed(t *testing.T) {
	hub, owner, bridge := bridgeFixtures(t)
	owner.f.pr.Mergeable = new(bool)
	*owner.f.pr.Mergeable = true
	owner.f.checks = forge.CIStatus{State: forge.CIStateSuccess, Checks: []forge.Check{{Name: "tests", State: forge.CIStateSuccess}}}
	f := &mergeForge{fakeForge: owner.f}
	owner.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return f, true }
	hub.link(t)
	bridge.err = platforms.ErrRemoteOffline
	hub.request(t, "pull-requests", mergeBody, 503)
	if f.merged != 0 || hub.f.reads != 0 {
		t.Fatal("disconnected owner fell back to the hub")
	}
	bridge.err = nil
	w := hub.request(t, "pull-requests", mergeBody, 200)
	var l Link
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	if f.merged != 1 || hub.f.reads != 0 || l.Platform != "r-box:opencode" || l.RemoteID != "box" || ownerLink(t, owner).PR.Status != "merged" {
		t.Fatalf("owner merge=%d link=%+v", f.merged, l)
	}
}
