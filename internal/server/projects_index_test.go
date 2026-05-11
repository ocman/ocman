package server

import (
	"testing"

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
