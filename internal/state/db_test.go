package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestGetSettingCancelledContext(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()

	database.db.SetMaxOpenConns(1)
	conn, err := database.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := database.db.Stats().WaitCount
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, _, err := database.GetSetting(ctx, "missing")
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for database.db.Stats().WaitCount == before && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if database.db.Stats().WaitCount == before {
		t.Fatal("GetSetting did not block waiting for the held connection")
	}
	cancel()
	err = <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetSetting error = %v, want context.Canceled", err)
	}
}

// openTestStateDB creates an in-memory state database with the schema
// migrated to the latest version.
func openTestStateDB(t *testing.T) *DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test state db: %v", err)
	}
	stateDB := &DB{db: sqlDB}
	if err := stateDB.init(); err != nil {
		sqlDB.Close()
		t.Fatalf("initializing state schema: %v", err)
	}
	return stateDB
}

// k is a tiny shorthand for constructing Key values in tests.
func k(platform, id string) Key { return Key{Platform: platform, SessionID: id} }

func TestSchemaCreation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// Verify tables exist by querying them
	var count int
	if err := db.db.QueryRow("SELECT count(*) FROM archived_session").Scan(&count); err != nil {
		t.Fatalf("archived_session table not created: %v", err)
	}
	if err := db.db.QueryRow("SELECT count(*) FROM seen_session").Scan(&count); err != nil {
		t.Fatalf("seen_session table not created: %v", err)
	}
}

func TestSchemaIdempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// Calling init again should not fail
	if err := db.init(); err != nil {
		t.Fatalf("second init() failed: %v", err)
	}
}

// --- Archive tests ---

func TestArchiveSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	archived, err := db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived session, got %d", len(archived))
	}
	if archived[k("opencode", "s1")] != 1000 {
		t.Errorf("expected timeUpdated=1000, got %d", archived[k("opencode", "s1")])
	}
}

func TestArchiveSession_Upsert(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 2000); err != nil {
		t.Fatalf("ArchiveSession (upsert): %v", err)
	}

	archived, err := db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if archived[k("opencode", "s1")] != 2000 {
		t.Errorf("expected timeUpdated=2000 after upsert, got %d", archived[k("opencode", "s1")])
	}
}

func TestArchiveProject(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveProject(t.Context(), "local", "/src/foo"); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	archived, err := db.ArchivedProjects(t.Context())
	if err != nil {
		t.Fatalf("ArchivedProjects: %v", err)
	}
	if _, ok := archived[ProjectKey{RemoteID: "local", Root: "/src/foo"}]; !ok {
		t.Fatalf("expected /src/foo archived, got %v", archived)
	}
	if archived[ProjectKey{RemoteID: "local", Root: "/src/foo"}] <= 0 {
		t.Errorf("expected positive archived_at, got %d", archived[ProjectKey{RemoteID: "local", Root: "/src/foo"}])
	}

	// Re-archive refreshes archived_at (upsert, no duplicate row).
	if err := db.ArchiveProject(t.Context(), "local", "/src/foo"); err != nil {
		t.Fatalf("ArchiveProject (upsert): %v", err)
	}
	archived, _ = db.ArchivedProjects(t.Context())
	if len(archived) != 1 {
		t.Errorf("expected 1 archived project after upsert, got %d", len(archived))
	}

	if err := db.UnarchiveProject(t.Context(), "local", "/src/foo"); err != nil {
		t.Fatalf("UnarchiveProject: %v", err)
	}
	archived, _ = db.ArchivedProjects(t.Context())
	if len(archived) != 0 {
		t.Errorf("expected 0 archived projects after unarchive, got %d", len(archived))
	}
}

func TestUnarchiveSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := db.UnarchiveSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatalf("UnarchiveSession: %v", err)
	}

	archived, err := db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if len(archived) != 0 {
		t.Errorf("expected 0 archived sessions after unarchive, got %d", len(archived))
	}
}

func TestUnarchiveSession_Nonexistent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.UnarchiveSession(t.Context(), "opencode", "nonexistent"); err != nil {
		t.Fatalf("UnarchiveSession on nonexistent: %v", err)
	}
}

// TestArchive_PerPlatformIsolation verifies that the same session-ID
// can be archived independently across platforms without either one
// clobbering the other.
func TestArchive_PerPlatformIsolation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession opencode: %v", err)
	}
	if err := db.ArchiveSession(t.Context(), "other-platform", "s1", 2000); err != nil {
		t.Fatalf("ArchiveSession other-platform: %v", err)
	}
	archived, err := db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if archived[k("opencode", "s1")] != 1000 {
		t.Errorf("opencode/s1: expected 1000, got %d", archived[k("opencode", "s1")])
	}
	if archived[k("other-platform", "s1")] != 2000 {
		t.Errorf("other-platform/s1: expected 2000, got %d", archived[k("other-platform", "s1")])
	}

	// Unarchiving one platform's entry must leave the other's alone.
	if err := db.UnarchiveSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatalf("UnarchiveSession opencode: %v", err)
	}
	archived, err = db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if _, ok := archived[k("opencode", "s1")]; ok {
		t.Error("opencode/s1 should be gone")
	}
	if archived[k("other-platform", "s1")] != 2000 {
		t.Error("other-platform/s1 should survive opencode unarchive")
	}
}

// --- Seen tests ---

func TestMarkSessionSeen(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}

	seen, err := db.SeenSessions(t.Context())
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected 1 seen session, got %d", len(seen))
	}
	if seen[k("opencode", "s1")] != 1000 {
		t.Errorf("expected timeUpdated=1000, got %d", seen[k("opencode", "s1")])
	}
}

func TestMarkSessionSeen_OnlyUpdatesIfNewer(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen(t.Context(), "opencode", "s1", 2000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}
	// Older timestamp should NOT downgrade.
	if err := db.MarkSessionSeen(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen (older): %v", err)
	}

	seen, err := db.SeenSessions(t.Context())
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if seen[k("opencode", "s1")] != 2000 {
		t.Errorf("expected timeUpdated=2000 (not downgraded), got %d", seen[k("opencode", "s1")])
	}
}

func TestMarkSessionSeen_UpdatesIfNewer(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen(t.Context(), "opencode", "s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}
	if err := db.MarkSessionSeen(t.Context(), "opencode", "s1", 3000); err != nil {
		t.Fatalf("MarkSessionSeen (newer): %v", err)
	}

	seen, err := db.SeenSessions(t.Context())
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if seen[k("opencode", "s1")] != 3000 {
		t.Errorf("expected timeUpdated=3000 (upgraded), got %d", seen[k("opencode", "s1")])
	}
}

func TestMultipleSessions(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 100); err != nil {
		t.Fatalf("ArchiveSession s1: %v", err)
	}
	if err := db.ArchiveSession(t.Context(), "opencode", "s2", 200); err != nil {
		t.Fatalf("ArchiveSession s2: %v", err)
	}
	if err := db.MarkSessionSeen(t.Context(), "opencode", "s3", 300); err != nil {
		t.Fatalf("MarkSessionSeen s3: %v", err)
	}

	archived, err := db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if len(archived) != 2 {
		t.Errorf("expected 2 archived sessions, got %d", len(archived))
	}

	seen, err := db.SeenSessions(t.Context())
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("expected 1 seen session, got %d", len(seen))
	}
}

// --- Open with file tests ---

func TestOpen_CreatesDirectoryAndSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nested", "dir", "state.db")

	stateDB, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer stateDB.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("database file was not created")
	}
	assertFileMode(t, filepath.Dir(dbPath), 0o700)
	assertFileMode(t, dbPath, 0o600)

	if err := stateDB.ArchiveSession(t.Context(), "opencode", "test", 123); err != nil {
		t.Fatalf("ArchiveSession after Open: %v", err)
	}
}

func TestOpen_RepairsPermissionsWithoutDataLoss(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	dbPath := filepath.Join(dir, "state.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := db.ArchiveSession(t.Context(), "opencode", "kept", 123); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dbPath, 0o666); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	archived, err := db.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if _, ok := archived[Key{Platform: "opencode", SessionID: "kept"}]; !ok {
		t.Fatal("existing state was not preserved")
	}
	assertFileMode(t, dir, 0o700)
	assertFileMode(t, dbPath, 0o600)
}

func TestSecureStatePaths_RepairsSidecars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "state.db")
	if err := secureStatePaths(dbPath); err != nil {
		t.Fatalf("secureStatePaths: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := dbPath + suffix
		if err := os.WriteFile(path, nil, 0o666); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	if err := secureStatePaths(dbPath); err != nil {
		t.Fatalf("repair sidecars: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		assertFileMode(t, dbPath+suffix, 0o600)
	}
}

func TestOpen_RejectsSymlinkedDatabase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("Open accepted a symlinked state database")
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}

func TestDefaultDBPath(t *testing.T) {
	path := DefaultDBPath()
	if path == "" {
		t.Fatal("DefaultDBPath returned empty string")
	}
	if filepath.Base(path) != "state.db" {
		t.Errorf("expected path ending in state.db, got %s", path)
	}
}

// --- Model favorite tests ---

func TestAddModelFavorite_RoundTrip(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.AddModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4"); err != nil {
		t.Fatalf("AddModelFavorite: %v", err)
	}

	favs, err := db.ModelFavorites(t.Context(), "opencode")
	if err != nil {
		t.Fatalf("ModelFavorites: %v", err)
	}
	if len(favs) != 1 {
		t.Fatalf("expected 1 favorite, got %d", len(favs))
	}
	if favs[0].Platform != "opencode" || favs[0].Provider != "anthropic" || favs[0].Model != "claude-opus-4" {
		t.Errorf("unexpected favorite: %+v", favs[0])
	}
}

func TestAddModelFavorite_Idempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	for i := 0; i < 3; i++ {
		if err := db.AddModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4"); err != nil {
			t.Fatalf("AddModelFavorite #%d: %v", i, err)
		}
	}

	favs, _ := db.ModelFavorites(t.Context(), "opencode")
	if len(favs) != 1 {
		t.Errorf("expected 1 favorite after repeated adds, got %d", len(favs))
	}
}

func TestRemoveModelFavorite(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_ = db.AddModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4")
	if err := db.RemoveModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4"); err != nil {
		t.Fatalf("RemoveModelFavorite: %v", err)
	}

	favs, _ := db.ModelFavorites(t.Context(), "opencode")
	if len(favs) != 0 {
		t.Errorf("expected 0 favorites after remove, got %d", len(favs))
	}
}

func TestRemoveModelFavorite_Nonexistent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// Removing a non-existent favorite is a no-op (no error).
	if err := db.RemoveModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4"); err != nil {
		t.Fatalf("RemoveModelFavorite on nonexistent: %v", err)
	}
}

// TestModelFavorites_PerPlatformIsolation verifies that the same
// (provider, model) pair can be favorited independently across
// platforms. Matches the archived_session / seen_session isolation
// guarantee.
func TestModelFavorites_PerPlatformIsolation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_ = db.AddModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4")
	_ = db.AddModelFavorite(t.Context(), "other-platform", "anthropic", "claude-opus-4")

	oc, _ := db.ModelFavorites(t.Context(), "opencode")
	cc, _ := db.ModelFavorites(t.Context(), "other-platform")
	if len(oc) != 1 || len(cc) != 1 {
		t.Fatalf("expected 1 favorite per platform, got opencode=%d, other-platform=%d", len(oc), len(cc))
	}

	// Unfavoriting one platform must leave the other alone.
	_ = db.RemoveModelFavorite(t.Context(), "opencode", "anthropic", "claude-opus-4")
	oc, _ = db.ModelFavorites(t.Context(), "opencode")
	cc, _ = db.ModelFavorites(t.Context(), "other-platform")
	if len(oc) != 0 {
		t.Errorf("opencode favorites should be empty, got %d", len(oc))
	}
	if len(cc) != 1 {
		t.Errorf("other-platform favorite should survive, got %d", len(cc))
	}
}

// TestModelFavorites_OrderedByCreatedAt checks that favorites come
// back in insertion order — the merger uses this to preserve the
// order the user starred them in.
func TestModelFavorites_OrderedByCreatedAt(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// Use raw inserts with explicit timestamps so the order is
	// unambiguous even if two AddModelFavorite calls happen in the
	// same millisecond on a fast machine.
	stmts := []struct {
		provider, model string
		ts              int64
	}{
		{"anthropic", "claude-opus-4", 100},
		{"openai", "gpt-5", 200},
		{"google", "gemini-pro", 150},
	}
	for _, s := range stmts {
		_, err := db.db.Exec(
			`INSERT INTO model_favorite (platform, provider_id, model_id, created_at) VALUES (?, ?, ?, ?)`,
			"opencode", s.provider, s.model, s.ts,
		)
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	favs, err := db.ModelFavorites(t.Context(), "opencode")
	if err != nil {
		t.Fatalf("ModelFavorites: %v", err)
	}
	if len(favs) != 3 {
		t.Fatalf("expected 3 favorites, got %d", len(favs))
	}
	wantOrder := []string{"claude-opus-4", "gemini-pro", "gpt-5"}
	for i, want := range wantOrder {
		if favs[i].Model != want {
			t.Errorf("position %d: expected %q, got %q", i, want, favs[i].Model)
		}
	}
}

// --- Pinned session tests ---

func TestPinSession_RoundTrip(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.PinSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatalf("PinSession: %v", err)
	}

	pinned, err := db.PinnedSessions(t.Context())
	if err != nil {
		t.Fatalf("PinnedSessions: %v", err)
	}
	if len(pinned) != 1 {
		t.Fatalf("expected 1 pinned session, got %d", len(pinned))
	}
	if pinnedAt, ok := pinned[k("opencode", "s1")]; !ok || pinnedAt <= 0 {
		t.Errorf("expected positive pinnedAt for opencode/s1, got %d", pinnedAt)
	}
}

func TestPinSession_Idempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.PinSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatalf("PinSession: %v", err)
	}
	firstPinned, _ := db.PinnedSessions(t.Context())
	firstAt := firstPinned[k("opencode", "s1")]

	// Pin again — should be a no-op (pinned_at unchanged).
	if err := db.PinSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatalf("PinSession (repeat): %v", err)
	}
	secondPinned, _ := db.PinnedSessions(t.Context())
	if len(secondPinned) != 1 {
		t.Errorf("expected 1 pinned session after repeat, got %d", len(secondPinned))
	}
	if secondPinned[k("opencode", "s1")] != firstAt {
		t.Errorf("pinned_at changed on repeat pin: %d → %d", firstAt, secondPinned[k("opencode", "s1")])
	}
}

func TestUnpinSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_ = db.PinSession(t.Context(), "opencode", "s1")
	if err := db.UnpinSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatalf("UnpinSession: %v", err)
	}

	pinned, _ := db.PinnedSessions(t.Context())
	if len(pinned) != 0 {
		t.Errorf("expected 0 pinned sessions after unpin, got %d", len(pinned))
	}
}

func TestUnpinSession_Nonexistent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.UnpinSession(t.Context(), "opencode", "nonexistent"); err != nil {
		t.Fatalf("UnpinSession on nonexistent: %v", err)
	}
}

func TestPinSession_PerPlatformIsolation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_ = db.PinSession(t.Context(), "opencode", "s1")
	_ = db.PinSession(t.Context(), "other-platform", "s1")

	pinned, _ := db.PinnedSessions(t.Context())
	if len(pinned) != 2 {
		t.Fatalf("expected 2 pinned sessions, got %d", len(pinned))
	}
	if _, ok := pinned[k("opencode", "s1")]; !ok {
		t.Error("opencode/s1 should be pinned")
	}
	if _, ok := pinned[k("other-platform", "s1")]; !ok {
		t.Error("other-platform/s1 should be pinned")
	}

	// Unpinning one platform must leave the other alone.
	_ = db.UnpinSession(t.Context(), "opencode", "s1")
	pinned, _ = db.PinnedSessions(t.Context())
	if _, ok := pinned[k("opencode", "s1")]; ok {
		t.Error("opencode/s1 should be gone")
	}
	if _, ok := pinned[k("other-platform", "s1")]; !ok {
		t.Error("other-platform/s1 should survive opencode unpin")
	}
}

func TestPinnedSessions_Empty(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	pinned, err := db.PinnedSessions(t.Context())
	if err != nil {
		t.Fatalf("PinnedSessions: %v", err)
	}
	if len(pinned) != 0 {
		t.Errorf("expected 0 pinned sessions on fresh db, got %d", len(pinned))
	}
}

// --- Setting tests (schema v12) ---

func TestSetting_GetMissingReturnsOkFalse(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	val, ok, err := db.GetSetting(t.Context(), "pr_prompt_template")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for missing key, got ok=true (val=%q)", val)
	}
	if val != "" {
		t.Errorf("expected empty value for missing key, got %q", val)
	}
}

func TestSetting_SetThenGet(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.SetSetting(t.Context(), "pr_prompt_template", "Handle PR #{number}"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	val, ok, err := db.GetSetting(t.Context(), "pr_prompt_template")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after SetSetting")
	}
	if val != "Handle PR #{number}" {
		t.Errorf("expected stored value, got %q", val)
	}
}

func TestSetting_OverwritesExistingValue(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.SetSetting(t.Context(), "issue_prompt_template", "first"); err != nil {
		t.Fatalf("SetSetting first: %v", err)
	}
	if err := db.SetSetting(t.Context(), "issue_prompt_template", "second"); err != nil {
		t.Fatalf("SetSetting second: %v", err)
	}

	val, ok, err := db.GetSetting(t.Context(), "issue_prompt_template")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if !ok || val != "second" {
		t.Errorf("expected (\"second\", true), got (%q, %v)", val, ok)
	}
}

func TestSetting_KeysAreIndependent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.SetSetting(t.Context(), "a", "alpha"); err != nil {
		t.Fatalf("SetSetting a: %v", err)
	}
	if err := db.SetSetting(t.Context(), "b", "beta"); err != nil {
		t.Fatalf("SetSetting b: %v", err)
	}

	a, _, err := db.GetSetting(t.Context(), "a")
	if err != nil {
		t.Fatalf("GetSetting a: %v", err)
	}
	b, _, err := db.GetSetting(t.Context(), "b")
	if err != nil {
		t.Fatalf("GetSetting b: %v", err)
	}
	if a != "alpha" || b != "beta" {
		t.Errorf("expected (alpha, beta), got (%q, %q)", a, b)
	}
}

func TestWorktreeInheritPermissions_DefaultsOn(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	on, err := db.GetWorktreeInheritPermissions(t.Context())
	if err != nil {
		t.Fatalf("GetWorktreeInheritPermissions: %v", err)
	}
	if !on {
		t.Error("expected default true when setting is absent")
	}
}

func TestWorktreeInheritPermissions_SetThenGet(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.SetWorktreeInheritPermissions(t.Context(), false); err != nil {
		t.Fatalf("SetWorktreeInheritPermissions(false): %v", err)
	}
	on, err := db.GetWorktreeInheritPermissions(t.Context())
	if err != nil {
		t.Fatalf("GetWorktreeInheritPermissions: %v", err)
	}
	if on {
		t.Error("expected false after disabling")
	}

	if err := db.SetWorktreeInheritPermissions(t.Context(), true); err != nil {
		t.Fatalf("SetWorktreeInheritPermissions(true): %v", err)
	}
	on, err = db.GetWorktreeInheritPermissions(t.Context())
	if err != nil {
		t.Fatalf("GetWorktreeInheritPermissions: %v", err)
	}
	if !on {
		t.Error("expected true after re-enabling")
	}
}
