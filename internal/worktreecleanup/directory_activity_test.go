package worktreecleanup

import (
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDirectoryActivityScopeAndPersistence(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.m.touchDirectory(ctx, "local", testPath); err != nil {
		t.Fatal(err)
	}
	want := f.now.UnixMilli()
	f.now = f.now.Add(-time.Second)
	if err := f.m.touchDirectory(ctx, "local", testPath+"/."); err != nil {
		t.Fatal(err)
	}
	// Rebuilding the module cannot forget a terminal's activity or shorten it
	// when the clock moves backwards. Names are path components, not prefixes.
	m := New(f.m.d)
	for _, tc := range []struct {
		owner, path string
		want        int64
	}{
		{"local", testPath, want},
		{"local", testPath + "/nested", want},
		{"local", testPath + "-other", 0},
		{"local", "/projects/.worktrees/repo", 0},
		{"box", testPath, 0},
	} {
		at, err := m.directoryActivity(ctx, tc.owner, tc.path)
		if err != nil || at != tc.want {
			t.Fatalf("owner=%s path=%s at=%d want=%d err=%v", tc.owner, tc.path, at, tc.want, err)
		}
	}
}

func TestDirectoryActivityRetainsCheckoutUntilGraceExpires(t *testing.T) {
	f := newFixture(t)
	_, release, err := f.m.BeginDirectory(t.Context(), testPath+"/nested")
	if err != nil {
		t.Fatal(err)
	}
	release()
	f.now = f.now.Add(retention - time.Millisecond)
	f.sweep(t, 0)
	f.now = f.now.Add(time.Millisecond)
	f.sweep(t, 1)
}

func TestOwnerRechecksDirectoryActivityBeforeInventory(t *testing.T) {
	for _, broken := range []bool{false, true} {
		f := newFixture(t)
		if err := f.m.touchDirectory(t.Context(), "local", testPath); err != nil {
			t.Fatal(err)
		}
		if broken {
			execFixtureSQL(t, f, "DROP TABLE worktree_cleanup_directory_activity")
		}
		err := f.m.verifyOwner(t.Context(), hostsvc.CleanupWorktreeRequest{WorktreeCheckpoint: hostsvc.WorktreeCheckpoint{Path: testPath}})
		if err == nil || !broken && !strings.Contains(err.Error(), "directory activity") {
			t.Fatalf("owner ignored directory safety metadata: %v", err)
		}
		f.sweep(t, 0)
	}
}

func TestDirectoryActivityMigrationPreservesSessionGrace(t *testing.T) {
	path := statetest.Path(t)
	previous := Schema
	previous.Tables = Schema.Tables[:2]
	previous.Migrations = Schema.Migrations[:1]
	store, err := state.Open(path, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQL().Exec(`INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at) VALUES('fake','s1',123)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var at int64
	if err := store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity WHERE session_id='s1'`).Scan(&at); err != nil || at != 123 {
		t.Fatalf("upgrade changed session grace: %d %v", at, err)
	}
	m := New(Deps{Store: store})
	if err := m.touchDirectory(t.Context(), "local", testPath); err != nil {
		t.Fatal(err)
	}
	if at, err := m.directoryActivity(t.Context(), "local", testPath); err != nil || at == 0 {
		t.Fatalf("upgraded directory activity unavailable: %d %v", at, err)
	}
}
