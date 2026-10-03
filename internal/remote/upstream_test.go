package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestUpstreamCacheMissingCheckoutRetainsSuccessWithoutCachingAbsence(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", "https://host/old.git"}} {
		if _, err := gitexec.Output(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	cache := newOriginCache()
	if _, err := cache.upstreams(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	entry := cache.m[dir]
	entry.expires = time.Time{}
	cache.m[dir] = entry
	parked := filepath.Join(t.TempDir(), "parked")
	if err := os.Rename(dir, parked); err != nil {
		t.Fatal(err)
	}
	if got, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Equal(got.keys, entry.keys) || !cache.m[dir].expires.IsZero() {
		t.Fatalf("missing checkout erased known keys or cached absence: %+v, %v", got, err)
	}
	if err := os.Rename(parked, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := gitexec.Output(t.Context(), dir, "remote", "set-url", "origin", "https://host/new.git"); err != nil {
		t.Fatal(err)
	}
	if got, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Equal(got.keys, []string{"host/new"}) {
		t.Fatalf("restored checkout could not retry immediately: %+v, %v", got, err)
	}
}

func TestUpstreamCacheCoalescesReadersAndDoesNotCacheFailures(t *testing.T) {
	cache := newOriginCache()
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	cache.read = func(context.Context, string) (string, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		<-release
		return "origin https://host/shared.git (fetch)", nil
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.upstreams(t.Context(), "/repo")
			if err != nil || !slices.Equal(got.keys, []string{"host/shared"}) {
				t.Errorf("shared discovery = %+v, %v", got, err)
			}
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond) // Let concurrent readers reach the blocked discovery.
	close(release)
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("concurrent discovery calls = %d", reads.Load())
	}
	failed := errors.New("git process unavailable")
	cache.read = func(context.Context, string) (string, error) { return "", failed }
	if _, err := cache.upstreams(t.Context(), "/new"); !errors.Is(err, failed) {
		t.Fatalf("discovery failure lost: %v", err)
	}
	if _, ok := cache.m["/new"]; ok {
		t.Fatal("failed discovery was cached")
	}
	cache.read = func(context.Context, string) (string, error) { return "", nil }
	if got, err := cache.upstreams(t.Context(), "/new"); err != nil || len(got.keys) != 0 {
		t.Fatalf("genuine empty remote set = %+v, %v", got, err)
	}
}

func TestEnrichProjectStatsCanceledScanPreservesInputAndCanRetry(t *testing.T) {
	stats := []db.ProjectStats{
		{Directory: t.TempDir(), UpstreamKeys: []string{"host/one"}},
		{Directory: t.TempDir(), UpstreamKeys: []string{"host/two"}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := EnrichProjectStats(ctx, stats); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan failure lost: %v", err)
	}
	if stats[0].UpstreamKeys[0] != "host/one" || stats[1].UpstreamKeys[0] != "host/two" {
		t.Fatal("canceled scan modified checkout identities")
	}
	for _, p := range stats {
		if _, err := gitexec.Output(t.Context(), p.Directory, "init"); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnrichProjectStats(t.Context(), stats); err != nil {
		t.Fatal(err)
	}
	if len(stats[0].UpstreamKeys) != 0 || len(stats[1].UpstreamKeys) != 0 {
		t.Fatal("successful retry did not discover genuine empty remote sets")
	}
}

func TestUpstreamCacheCanceledRefreshRetainsSuccessAndRetries(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", "https://host/old.git"}} {
		if _, err := gitexec.Output(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	cache := newOriginCache()
	if _, err := cache.upstreams(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	entry := cache.m[dir]
	entry.expires = time.Time{}
	cache.m[dir] = entry
	cache.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stale, err := cache.upstreams(ctx, dir)
	if !errors.Is(err, context.Canceled) || !slices.Equal(stale.keys, []string{"host/old"}) {
		t.Fatalf("canceled refresh = %+v, %v", stale, err)
	}
	cache.mu.Lock()
	kept := cache.m[dir]
	cache.mu.Unlock()
	if !slices.Equal(kept.keys, []string{"host/old"}) || !kept.expires.IsZero() {
		t.Fatalf("canceled refresh poisoned successful cache: %+v", kept)
	}
	if _, err := gitexec.Output(t.Context(), dir, "remote", "set-url", "origin", "https://host/new.git"); err != nil {
		t.Fatal(err)
	}
	if got, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Equal(got.keys, []string{"host/new"}) {
		t.Fatalf("successful retry did not refresh immediately: %+v, %v", got, err)
	}
}

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
	upstreams, err := cache.upstreams(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := projectIdentities([]db.ProjectStats{{Directory: dir, UpstreamKeys: upstreams.keys, UpstreamOrigin: NormalizeUpstream(upstreams.origin)}})
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
	if warm, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Equal(warm.keys, want) {
		t.Fatal("warm cache changed before expiry")
	}
	cache.mu.Lock()
	entry := cache.m[dir]
	entry.expires = time.Time{}
	cache.m[dir] = entry
	cache.mu.Unlock()
	if fresh, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Contains(fresh.keys, "github.com/org/changed") {
		t.Fatal("expired cache did not refresh remotes")
	}
	stats := []db.ProjectStats{{Directory: dir}, {Directory: filepath.Join(dir, "missing")}}
	if err := EnrichProjectStats(t.Context(), stats); err != nil {
		t.Fatal(err)
	}
	if len(stats[0].UpstreamKeys) != 3 || len(stats[1].UpstreamKeys) != 0 {
		t.Fatalf("enriched stats = %+v", stats)
	}
}
