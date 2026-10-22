package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type timeoutArchivePlatform struct {
	fakePlatform
	budget time.Duration
}

func (p *timeoutArchivePlatform) SessionsInactiveBefore(ctx context.Context, _ int64) ([]db.SessionArchiveCandidate, error) {
	deadline, _ := ctx.Deadline()
	p.budget = time.Until(deadline)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestArchiveRemoteTimeoutDoesNotStarveLocalMaintenance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := testServer(t)
	project := t.TempDir()
	srv.projects.loaded = true
	srv.projects.data = []db.ProjectStats{{Directory: project, LastUsed: time.Now().Add(-14 * 24 * time.Hour).UnixMilli()}}
	remote := &timeoutArchivePlatform{}
	srv.registry = platforms.NewRegistry()
	srv.registry.Register(remote)
	file := filepath.Join(composerAttachmentRoot(), "project", "session", "expired")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("expired"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * composerAttachmentTTL)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if autoArchivePlatformTimeout != 10*time.Second {
		t.Fatalf("autoArchivePlatformTimeout = %v, want 10s", autoArchivePlatformTimeout)
	}
	previous := autoArchivePlatformTimeout
	t.Cleanup(func() { autoArchivePlatformTimeout = previous })
	autoArchivePlatformTimeout = 100 * time.Millisecond
	autoArchiveTickFn(t.Context(), srv)
	if remote.budget <= 0 || remote.budget > autoArchivePlatformTimeout {
		t.Errorf("remote RPC budget=%v, want <=%v", remote.budget, autoArchivePlatformTimeout)
	}
	archived, err := srv.stateDB.ArchivedProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[state.ProjectKey{RemoteID: state.LocalRemoteID, Root: projectRootForDirectory(project)}]; !ok {
		t.Error("remote timeout prevented local project archiving")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("remote timeout prevented attachment cleanup: %v", err)
	}
}

type snoozeDuringArchivePlatform struct {
	fakePlatform
	store *state.DB
	until int64
	wake  bool
}

func (p *snoozeDuringArchivePlatform) SessionsInactiveBefore(ctx context.Context, _ int64) ([]db.SessionArchiveCandidate, error) {
	// The archive snapshot has already been read when the adapter is called.
	if err := p.store.SnoozeSession(ctx, string(p.ID()), "s1", p.until); err != nil {
		return nil, err
	}
	if p.wake {
		if _, err := p.store.WakeSnoozedSessions(ctx, p.until); err != nil {
			return nil, err
		}
	}
	return []db.SessionArchiveCandidate{{ID: "s1", TimeUpdated: 1}}, nil
}

func TestAutoArchivePreservesConcurrentSnooze(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t), registry: platforms.NewRegistry()}
	until := time.Now().Add(time.Hour).UnixMilli()
	srv.registry.Register(&snoozeDuringArchivePlatform{fakePlatform: fakePlatform{id: "fake"}, store: srv.stateDB, until: until})
	srv.autoArchiveInactiveSessions(t.Context())
	keys, err := srv.stateDB.WakeSnoozedSessions(t.Context(), until)
	if err != nil || len(keys) != 1 || keys[0] != (state.Key{Platform: "fake", SessionID: "s1"}) {
		t.Fatalf("auto-archive erased accepted snooze: %v %v", keys, err)
	}
}

func TestAutoArchivePreservesSnoozeThatWakesDuringScan(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t), registry: platforms.NewRegistry()}
	srv.registry.Register(&snoozeDuringArchivePlatform{fakePlatform: fakePlatform{id: "fake"}, store: srv.stateDB, until: time.Now().Add(time.Hour).UnixMilli(), wake: true})
	srv.autoArchiveInactiveSessions(t.Context())
	archivedSessions, err := srv.stateDB.ArchivedSessions(t.Context())
	_, archived := archivedSessions[state.Key{Platform: "fake", SessionID: "s1"}]
	if err != nil || archived {
		t.Fatalf("auto-archive re-hid awake session: %v %v", archived, err)
	}
}
