package remote

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestNormalizeUpstream(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"git@GitHub.com:Org/Repo.git", "github.com/org/repo"},
		{"https://user:secret@github.com/Org/Repo.git/", "github.com/org/repo"},
		{"ssh://git@code.example:22/Group/Repo.git", "code.example/Group/Repo"},
		{"https://code.example:443/Group/Repo", "code.example/Group/Repo"},
		{"ssh://git@code.example:2222/Group/Repo", "code.example:2222/Group/Repo"},
		{"git://code.example:9418/org/repo", "code.example/org/repo"},
		{"http://code.example:80/org/repo", "code.example/org/repo"},
		{"ssh://git@[::1]:2222/org/repo", "[::1]:2222/org/repo"},
		{"/local/repo", ""}, {"../repo", ""}, {"file:///local/repo", ""},
		{"https://host/", ""}, {"https://host/a/../b", ""},
		{"https://host/a//b", ""}, {"https://host/a?token=secret", ""},
		{"https://host/a#ref", ""}, {"https://host:bad/a", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := NormalizeUpstream(tc.raw); got != tc.want {
				t.Fatalf("NormalizeUpstream = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpstreamInventoryCache(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := gitexec.Output(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	run("init")
	run("remote", "add", "origin", "https://user:secret@github.com/Org/Fork.git")
	run("remote", "add", "upstream", "git@github.com:Org/Shared.git")
	run("remote", "set-url", "--push", "origin", "https://github.com/Org/PushOnly.git")
	run("remote", "add", "duplicate", "https://github.com/org/shared")
	cache := newOriginCache()
	got := projectIdentities(t.Context(), cache, []db.ProjectStats{{Directory: dir}})
	want := []string{"github.com/org/fork", "github.com/org/shared"}
	if len(got) != 1 || !slices.Equal(got[0].UpstreamKeys, want) {
		t.Fatalf("inventory = %+v", got)
	}
	if got[0].Origin != "github.com/org/fork" {
		t.Fatalf("origin exposed credentials: %q", got[0].Origin)
	}
	// Consumers cannot modify the cache's upstream slice.
	got[0].UpstreamKeys[0] = "modified"
	run("remote", "set-url", "upstream", "https://github.com/Org/Changed.git")
	if !slices.Equal(cache.upstreams(t.Context(), dir).keys, want) {
		t.Fatal("warm cache changed before expiry")
	}
	cache.mu.Lock()
	entry := cache.m[dir]
	entry.expires = time.Time{}
	cache.m[dir] = entry
	cache.mu.Unlock()
	if !slices.Contains(cache.upstreams(t.Context(), dir).keys, "github.com/org/changed") {
		t.Fatal("expired cache did not refresh remotes")
	}
	stats := []db.ProjectStats{{Directory: dir}, {Directory: filepath.Join(dir, "missing")}}
	EnrichProjectStats(t.Context(), stats)
	if len(stats[0].UpstreamKeys) != 3 || len(stats[1].UpstreamKeys) != 0 {
		t.Fatalf("enriched stats = %+v", stats)
	}
}
