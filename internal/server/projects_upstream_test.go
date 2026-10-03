package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestHostProjectsLoadedSnapshotDoesNotDiscoverUpstreams(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf called > \""+marker+"\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv := New(nil, nil, "", nil, nil)
	want := []db.ProjectStats{{Directory: t.TempDir(), UpstreamKeys: []string{"host/shared"}}}
	srv.projects.loaded = true
	srv.projects.data = want
	got, err := srv.hostProjects(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded snapshot changed: %+v, %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("loaded snapshot ran upstream discovery")
	}
}

func TestProjectsRefreshRetainsSnapshotOnDiscoveryFailure(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	want := []db.ProjectStats{{Directory: "/repo", SessionCount: 1, UpstreamKeys: []string{"host/old"}}}
	srv.projects.loaded = true
	srv.projects.data = want
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/repo", SessionCount: 2}}, nil
	}
	fail := true
	srv.projects.enrich = func(_ context.Context, stats []db.ProjectStats) error {
		stats[0].UpstreamKeys = []string{"host/new"}
		if fail {
			return context.Canceled
		}
		return nil
	}
	if err := srv.refreshProjectsIndex(); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery error lost: %v", err)
	}
	got, loaded, dirty := srv.projectsSnapshotState()
	if !loaded || !dirty || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed discovery published a partial snapshot: %+v", got)
	}
	fail = false
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatal(err)
	}
	got, _, dirty = srv.projectsSnapshotState()
	if dirty || got[0].SessionCount != 2 || got[0].UpstreamKeys[0] != "host/new" {
		t.Fatalf("retry did not publish enriched stats: %+v", got)
	}
}

func TestHostProjectsReturnsStaleWhileDiscoveryIsBlocked(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	want := []db.ProjectStats{{Directory: "/repo", UpstreamKeys: []string{"host/old"}}}
	srv.projects.loaded = true
	srv.projects.dirty = true
	srv.projects.data = want
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}
	started, release := make(chan struct{}), make(chan struct{})
	srv.projects.enrich = func(_ context.Context, stats []db.ProjectStats) error {
		close(started)
		<-release
		stats[0].UpstreamKeys = []string{"host/new"}
		return nil
	}
	defer func() { close(release); waitProjectsRefresh(t, srv) }()
	got, err := srv.hostProjects(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("stale request = %+v, %v", got, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background upstream discovery did not start")
	}
	got, err = srv.hostProjects(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("request during discovery = %+v, %v", got, err)
	}
}
