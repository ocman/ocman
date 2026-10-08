package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// countingRunner forwards to the real git binary and records how many
// subprocesses ran, and how many ran at once.
type countingRunner struct {
	mu       sync.Mutex
	probes   int
	inflight int
	max      int
	delay    time.Duration
}

func (r *countingRunner) read(ctx context.Context, dir string) (string, error) {
	r.mu.Lock()
	r.probes++
	r.inflight++
	if r.inflight > r.max {
		r.max = r.inflight
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inflight--
		r.mu.Unlock()
	}()
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	return gitexec.Output(ctx, dir, "remote", "-v")
}

func (r *countingRunner) counts() (probes, max int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.probes, r.max
}

func initRepoWithRemote(t *testing.T, dir, url string) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", url}} {
		if _, err := gitexec.Output(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpstreamCacheUnchangedRepoNotReprobedWithinTTL(t *testing.T) {
	dir := t.TempDir()
	initRepoWithRemote(t, dir, "https://host/old.git")
	runner := &countingRunner{}
	cache := newOriginCache()
	cache.read = runner.read
	for range 2 {
		got, err := cache.upstreams(t.Context(), dir)
		if err != nil || !slices.Equal(got.keys, []string{"host/old"}) {
			t.Fatalf("upstreams = %+v, %v", got, err)
		}
	}
	if probes, _ := runner.counts(); probes != 1 {
		t.Fatalf("git probes for unchanged repo = %d, want 1", probes)
	}
	// The tick is 5 minutes (server.projectsScanInterval); an entry that
	// expires on or before the next tick is rebuilt every refresh.
	cache.mu.Lock()
	ttl := time.Until(cache.m[dir].expires)
	cache.mu.Unlock()
	if ttl <= 5*time.Minute {
		t.Fatalf("cache TTL %v never outlives the 5m refresh tick", ttl)
	}
}

func TestUpstreamCacheRemoteChangeReflectedWithinTTL(t *testing.T) {
	dir := t.TempDir()
	initRepoWithRemote(t, dir, "https://host/old.git")
	runner := &countingRunner{}
	cache := newOriginCache()
	cache.read = runner.read
	if _, err := cache.upstreams(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := gitexec.Output(t.Context(), dir, "remote", "set-url", "origin", "https://host/new.git"); err != nil {
		t.Fatal(err)
	}
	got, err := cache.upstreams(t.Context(), dir)
	if err != nil || !slices.Equal(got.keys, []string{"host/new"}) {
		t.Fatalf("remote change not reflected in warm cache: %+v, %v", got, err)
	}
}

func TestUpstreamCacheLinkedWorktreeAndSubdirectory(t *testing.T) {
	dir := t.TempDir()
	initRepoWithRemote(t, dir, "https://host/old.git")
	linked := filepath.Join(t.TempDir(), "linked")
	for _, args := range [][]string{
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial"},
		{"worktree", "add", "-b", "linked", linked},
	} {
		if _, err := gitexec.Output(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := newOriginCache()
	for _, target := range []string{linked, subdir} {
		if got, err := cache.upstreams(t.Context(), target); err != nil || got.origin != "https://host/old.git" {
			t.Fatalf("initial upstream for %s = %+v, %v", target, got, err)
		}
	}
	if _, err := gitexec.Output(t.Context(), dir, "remote", "set-url", "origin", "https://host/new.git"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{linked, subdir} {
		if got, err := cache.upstreams(t.Context(), target); err != nil || got.origin != "https://host/new.git" {
			t.Fatalf("changed upstream for %s = %+v, %v", target, got, err)
		}
	}
}

func TestUpstreamCacheMissingCheckoutCachesAbsenceAndRedetectsRestore(t *testing.T) {
	dir := t.TempDir()
	initRepoWithRemote(t, dir, "https://host/old.git")
	runner := &countingRunner{}
	cache := newOriginCache()
	cache.read = runner.read
	if _, err := cache.upstreams(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	parked := filepath.Join(t.TempDir(), "parked")
	if err := os.Rename(dir, parked); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, err := cache.upstreams(t.Context(), dir)
		if err != nil || !slices.Equal(got.keys, []string{"host/old"}) {
			t.Fatalf("missing checkout erased known keys: %+v, %v", got, err)
		}
	}
	// One extra probe classifies the deletion; the absence is then cached.
	if probes, _ := runner.counts(); probes != 2 {
		t.Fatalf("missing checkout was re-probed every call: %d git calls", probes)
	}
	if err := os.Rename(parked, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := gitexec.Output(t.Context(), dir, "remote", "set-url", "origin", "https://host/new.git"); err != nil {
		t.Fatal(err)
	}
	got, err := cache.upstreams(t.Context(), dir)
	if err != nil || !slices.Equal(got.keys, []string{"host/new"}) {
		t.Fatalf("restored checkout not re-detected: %+v, %v", got, err)
	}
}

func TestUpstreamCacheNonRepoDirectoryCachedUntilRecheck(t *testing.T) {
	dir := t.TempDir()
	runner := &countingRunner{}
	cache := newOriginCache()
	cache.read = runner.read
	for range 3 {
		got, err := cache.upstreams(t.Context(), dir)
		if err != nil || len(got.keys) != 0 {
			t.Fatalf("non-repo directory = %+v, %v", got, err)
		}
	}
	if probes, _ := runner.counts(); probes != 1 {
		t.Fatalf("non-repo directory was re-probed every call: %d git calls", probes)
	}
	if entry := cache.m[dir]; entry.gone || time.Since(entry.expires.Add(-nonRepoRecheck)) > time.Minute {
		t.Fatalf("non-repo recheck interval not applied: %+v", entry)
	}
	initRepoWithRemote(t, dir, "https://host/new.git")
	if _, err := cache.upstreams(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if probes, _ := runner.counts(); probes != 1 {
		t.Fatalf("recreated repository probed before recheck interval: %d", probes)
	}
	cache.mu.Lock()
	entry := cache.m[dir]
	entry.expires = time.Time{}
	cache.m[dir] = entry
	cache.mu.Unlock()
	if got, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Equal(got.keys, []string{"host/new"}) {
		t.Fatalf("recreated repository not picked up on recheck: %+v, %v", got, err)
	}
}

func TestEnrichProjectStatsBoundedParallelismAndDeterministic(t *testing.T) {
	prev := projectUpstreamsCache
	defer func() { projectUpstreamsCache = prev }()
	projectUpstreamsCache = newOriginCache()

	const dirs = 24
	base := t.TempDir()
	var stats []db.ProjectStats
	for i := range dirs {
		dir := filepath.Join(base, fmt.Sprintf("repo-%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		initRepoWithRemote(t, dir, fmt.Sprintf("https://host/repo-%02d.git", i))
		stats = append(stats, db.ProjectStats{Directory: dir})
	}
	runner := &countingRunner{delay: 10 * time.Millisecond}
	projectUpstreamsCache.read = runner.read
	if err := EnrichProjectStats(t.Context(), stats); err != nil {
		t.Fatal(err)
	}
	probes, max := runner.counts()
	if probes != dirs {
		t.Fatalf("probes = %d, want %d", probes, dirs)
	}
	if max < 2 || max > enrichParallelism {
		t.Fatalf("max concurrent git lookups = %d, want 2..%d", max, enrichParallelism)
	}
	for i, p := range stats {
		want := fmt.Sprintf("host/repo-%02d", i)
		if !slices.Equal(p.UpstreamKeys, []string{want}) || p.UpstreamOrigin != want {
			t.Fatalf("stats[%d] = %+v, want %q", i, p, want)
		}
	}
	if err := EnrichProjectStats(t.Context(), stats); err != nil {
		t.Fatal(err)
	}
	if probes, _ := runner.counts(); probes != dirs {
		t.Fatalf("warm refresh launched additional git lookups: %d", probes)
	}

	// Deterministic: a second cold enrichment matches the first.
	second := make([]db.ProjectStats, len(stats))
	copy(second, stats)
	for i := range second {
		second[i].UpstreamKeys = nil
		second[i].UpstreamOrigin = ""
	}
	projectUpstreamsCache = newOriginCache()
	projectUpstreamsCache.read = runner.read
	if err := EnrichProjectStats(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stats, second) {
		t.Fatal("cold and warm enrichments disagree")
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
	if warm, err := cache.upstreams(t.Context(), dir); err != nil || !slices.Contains(warm.keys, "github.com/org/changed") {
		t.Fatalf("config change not reflected in warm cache: %+v, %v", warm, err)
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
