package remote

import (
	"context"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
)

func TestPRSidebarSnapshotsReplaceLinksAndRetainLastSuccess(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	responses := make(chan platforms.PullRequestResponse, 3)
	responses <- platforms.PullRequestResponse{Status: 200, Body: []byte(`[{"sessionId":"s1","number":42}]`)}
	responses <- platforms.PullRequestResponse{Status: 503}
	responses <- platforms.PullRequestResponse{Status: 200, Body: []byte(`[]`)}
	srv := NewServer(reg, localStubHost{}, "owner", "test").UsePullRequests(func(_ context.Context, platform string, req platforms.PullRequestRequest) platforms.PullRequestResponse {
		if platform != "opencode" || req.Operation != "sidebar-links" || req.SessionID != "" {
			t.Errorf("unexpected snapshot request: %s %+v", platform, req)
			return platforms.PullRequestResponse{Status: http.StatusBadRequest}
		}
		return <-responses
	})
	p := newRemotePlatform(connectWorkOwner(t, srv), "opencode", func() string { return "Owner" })
	if _, ready := p.PullRequestSidebar(); ready {
		t.Fatal("uninitialized cache reported ready")
	}
	for i, want := range []int{1, 1, 0} {
		if !p.syncPRSidebar(t.Context()) {
			t.Fatal("unexpected worker stop")
		}
		if rows, ready := p.PullRequestSidebar(); !ready || len(rows) != want {
			t.Fatalf("sync %d: rows=%+v ready=%t", i, rows, ready)
		}
	}
}

func TestCachedForgeRemotesAreScopedToTheCheckoutOwner(t *testing.T) {
	m := &Manager{inventory: map[string][]ProjectIdentity{
		"box":   {{Dir: "/repo", ForgeRemotes: []forge.Remote{{Name: "upstream", Host: "github.com", Repo: "Org/Repo"}}}},
		"other": {{Dir: "/repo", ForgeRemotes: []forge.Remote{{Name: "origin", Host: "github.com", Repo: "Other/Repo"}}}},
	}}
	if rows, ok := m.CachedForgeRemotes("box", "/repo/.worktrees/repo/topic", "/repo"); !ok || len(rows) != 1 || rows[0].Name != "upstream" || rows[0].Repo != "Org/Repo" {
		t.Fatalf("worktree remotes=%+v ok=%t", rows, ok)
	}
	if rows, ok := m.CachedForgeRemotes("unknown", "/repo", "/repo"); ok || len(rows) != 0 {
		t.Fatalf("borrowed remotes=%+v ok=%t", rows, ok)
	}
}
