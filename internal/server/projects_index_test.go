package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// TestRefreshProjectsIndex_SetsLoadedFlag asserts that after a successful
// refresh the loaded flag is true and projectsSnapshot reports loaded.
func TestRefreshProjectsIndex_SetsLoadedFlag(t *testing.T) {
	srv := testServer(t)

	_, loaded := srv.projectsSnapshot()
	if loaded {
		t.Fatal("expected loaded=false before first refresh")
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("refreshProjectsIndex: %v", err)
	}

	_, loaded = srv.projectsSnapshot()
	if !loaded {
		t.Error("expected loaded=true after successful refresh")
	}
}

func TestProjectsBackgroundRefreshRequiresDemand(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	var calls atomic.Int32
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		calls.Add(1)
		return nil, nil
	}

	srv.runProjectsIndexTick()
	srv.refreshProjectsIndexAsync()
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("background refreshes without demand = %d, want 0", got)
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("direct refresh calls = %d, want 1", got)
	}

	if err := srv.activity.Update(clientActivityLease{ClientID: "client", Visible: true, Scopes: []string{"projects"}, TTLMS: 45_000}); err != nil {
		t.Fatal(err)
	}
	srv.runProjectsIndexTick()
	if got := calls.Load(); got != 2 {
		t.Fatalf("background refreshes with demand = %d, want 2", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.runProjectsIndexLoop(ctx)
}

func TestHostProjectsRefreshesSkippedAsyncWork(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	calls := 0
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		calls++
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}
	srv.refreshProjectsIndexAsync()
	if calls != 0 {
		t.Fatalf("headless async refresh calls = %d, want 0", calls)
	}

	projects, err := srv.hostProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(projects) != 1 || projects[0].Directory != "/repo" {
		t.Fatalf("request refresh = calls %d, projects %#v", calls, projects)
	}
}

func TestHostProjectsRefreshesAfterSkippedTick(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		close(started)
		<-release
		return []db.ProjectStats{{Directory: "/fresh"}}, nil
	}
	srv.projects.loaded = true
	srv.projects.data = []db.ProjectStats{{Directory: "/stale"}}

	srv.runProjectsIndexTick()
	projects, err := srv.hostProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Directory != "/stale" {
		t.Fatalf("request projects = %#v, want stale snapshot", projects)
	}
	<-started
	close(release)
	waitProjectsRefresh(t, srv)
	projects, _ = srv.projectsSnapshot()
	if len(projects) != 1 || projects[0].Directory != "/fresh" {
		t.Fatalf("refreshed projects = %#v", projects)
	}
}

func TestLoadProjectsIndexCache(t *testing.T) {
	srv := testServer(t)
	want := []db.ProjectStats{{Directory: "/cached", SessionCount: 2}}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	refreshedAt := time.UnixMilli(1234)
	if err := srv.stateDB.SaveProjectsCache(t.Context(), data, refreshedAt); err != nil {
		t.Fatal(err)
	}

	srv.loadProjectsIndexCache(t.Context())
	projects, loaded, dirty := srv.projectsSnapshotState()
	if !loaded || !dirty || len(projects) != 1 || projects[0] != want[0] {
		t.Fatalf("loaded=%v dirty=%v projects=%#v", loaded, dirty, projects)
	}
	if !srv.projects.refreshedAt.Equal(refreshedAt) {
		t.Fatalf("refreshedAt = %v, want %v", srv.projects.refreshedAt, refreshedAt)
	}
}

func TestRefreshProjectsIndexPersistsCache(t *testing.T) {
	srv := testServer(t)
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/fresh", SessionCount: 2}}, nil
	}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatal(err)
	}

	srv.projects = projectsIndexState{}
	srv.loadProjectsIndexCache(t.Context())
	projects, loaded := srv.projectsSnapshot()
	if !loaded || len(projects) != 1 || projects[0].Directory != "/fresh" || projects[0].SessionCount != 2 {
		t.Fatalf("loaded=%v projects=%#v", loaded, projects)
	}
}

func TestRefreshProjectsIndexBroadcastsOnlyChanges(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.ch:
		if event.event != "ocman.projects.changed" {
			t.Fatalf("event = %q", event.event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projects changed event")
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.ch:
		t.Fatalf("unexpected unchanged event: %q", event.event)
	default:
	}
}

func waitProjectsRefresh(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		srv.projects.mu.RLock()
		running := srv.projects.running
		srv.projects.mu.RUnlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for projects refresh")
}

// TestRefreshProjectsIndex_NilDB is a no-op guard: when the server has
// no database, refreshProjectsIndex must return nil without panicking.
func TestRefreshProjectsIndex_NilDB(t *testing.T) {
	srv := &Server{}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Errorf("expected nil error with nil db, got %v", err)
	}
}

// TestProjectsSnapshot_ReturnsClone verifies that mutations to the returned
// slice do not corrupt the index's internal state (defensive copy contract).
func TestProjectsSnapshot_ReturnsClone(t *testing.T) {
	srv := testServer(t)

	srv.projects.mu.Lock()
	srv.projects.data = []db.ProjectStats{
		{Directory: "/repo/a"},
		{Directory: "/repo/b"},
	}
	srv.projects.loaded = true
	srv.projects.mu.Unlock()

	snap1, _ := srv.projectsSnapshot()
	if len(snap1) != 2 {
		t.Fatalf("want 2 entries, got %d", len(snap1))
	}

	// Mutate the returned slice; the internal state must be unchanged.
	snap1[0].Directory = "/mutated"

	snap2, _ := srv.projectsSnapshot()
	if snap2[0].Directory == "/mutated" {
		t.Error("projectsSnapshot must return an independent copy, not a reference to internal state")
	}
}

// TestCloneProjectStats_EmptyReturnsNil ensures the helper returns nil
// (not an empty slice) for empty input, matching the DB convention.
func TestCloneProjectStats_EmptyReturnsNil(t *testing.T) {
	if got := cloneProjectStats(nil); got != nil {
		t.Errorf("cloneProjectStats(nil) = %v, want nil", got)
	}
	if got := cloneProjectStats([]db.ProjectStats{}); got != nil {
		t.Errorf("cloneProjectStats([]) = %v, want nil", got)
	}
}

// TestCloneProjectStats_CopiesEntries verifies values are preserved and
// the clone is independent from the source.
func TestCloneProjectStats_CopiesEntries(t *testing.T) {
	src := []db.ProjectStats{{Directory: "/a"}, {Directory: "/b"}}
	dst := cloneProjectStats(src)
	if len(dst) != 2 || dst[0].Directory != "/a" || dst[1].Directory != "/b" {
		t.Errorf("unexpected clone result: %+v", dst)
	}
	src[0].Directory = "/changed"
	if dst[0].Directory != "/a" {
		t.Error("clone shares memory with source")
	}
}
