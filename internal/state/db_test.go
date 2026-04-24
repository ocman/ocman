package state

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// openTestStateDB creates an in-memory state database with the schema
// migrated to the latest version.
func openTestStateDB(t *testing.T) *DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", ":memory:")
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

	if err := db.ArchiveSession("opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	archived, err := db.ArchivedSessions()
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

	if err := db.ArchiveSession("opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := db.ArchiveSession("opencode", "s1", 2000); err != nil {
		t.Fatalf("ArchiveSession (upsert): %v", err)
	}

	archived, err := db.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if archived[k("opencode", "s1")] != 2000 {
		t.Errorf("expected timeUpdated=2000 after upsert, got %d", archived[k("opencode", "s1")])
	}
}

func TestUnarchiveSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession("opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := db.UnarchiveSession("opencode", "s1"); err != nil {
		t.Fatalf("UnarchiveSession: %v", err)
	}

	archived, err := db.ArchivedSessions()
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

	if err := db.UnarchiveSession("opencode", "nonexistent"); err != nil {
		t.Fatalf("UnarchiveSession on nonexistent: %v", err)
	}
}

// TestArchive_PerPlatformIsolation verifies that the same session-ID
// can be archived independently across platforms without either one
// clobbering the other.
func TestArchive_PerPlatformIsolation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession("opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession opencode: %v", err)
	}
	if err := db.ArchiveSession("claude-code", "s1", 2000); err != nil {
		t.Fatalf("ArchiveSession claude-code: %v", err)
	}
	archived, err := db.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if archived[k("opencode", "s1")] != 1000 {
		t.Errorf("opencode/s1: expected 1000, got %d", archived[k("opencode", "s1")])
	}
	if archived[k("claude-code", "s1")] != 2000 {
		t.Errorf("claude-code/s1: expected 2000, got %d", archived[k("claude-code", "s1")])
	}

	// Unarchiving one platform's entry must leave the other's alone.
	if err := db.UnarchiveSession("opencode", "s1"); err != nil {
		t.Fatalf("UnarchiveSession opencode: %v", err)
	}
	archived, err = db.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if _, ok := archived[k("opencode", "s1")]; ok {
		t.Error("opencode/s1 should be gone")
	}
	if archived[k("claude-code", "s1")] != 2000 {
		t.Error("claude-code/s1 should survive opencode unarchive")
	}
}

// --- Seen tests ---

func TestMarkSessionSeen(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen("opencode", "s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}

	seen, err := db.SeenSessions()
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

	if err := db.MarkSessionSeen("opencode", "s1", 2000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}
	// Older timestamp should NOT downgrade.
	if err := db.MarkSessionSeen("opencode", "s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen (older): %v", err)
	}

	seen, err := db.SeenSessions()
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

	if err := db.MarkSessionSeen("opencode", "s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}
	if err := db.MarkSessionSeen("opencode", "s1", 3000); err != nil {
		t.Fatalf("MarkSessionSeen (newer): %v", err)
	}

	seen, err := db.SeenSessions()
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

	if err := db.ArchiveSession("opencode", "s1", 100); err != nil {
		t.Fatalf("ArchiveSession s1: %v", err)
	}
	if err := db.ArchiveSession("opencode", "s2", 200); err != nil {
		t.Fatalf("ArchiveSession s2: %v", err)
	}
	if err := db.MarkSessionSeen("opencode", "s3", 300); err != nil {
		t.Fatalf("MarkSessionSeen s3: %v", err)
	}

	archived, err := db.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if len(archived) != 2 {
		t.Errorf("expected 2 archived sessions, got %d", len(archived))
	}

	seen, err := db.SeenSessions()
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

	if err := stateDB.ArchiveSession("opencode", "test", 123); err != nil {
		t.Fatalf("ArchiveSession after Open: %v", err)
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

	if err := db.AddModelFavorite("opencode", "anthropic", "claude-opus-4"); err != nil {
		t.Fatalf("AddModelFavorite: %v", err)
	}

	favs, err := db.ModelFavorites("opencode")
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
		if err := db.AddModelFavorite("opencode", "anthropic", "claude-opus-4"); err != nil {
			t.Fatalf("AddModelFavorite #%d: %v", i, err)
		}
	}

	favs, _ := db.ModelFavorites("opencode")
	if len(favs) != 1 {
		t.Errorf("expected 1 favorite after repeated adds, got %d", len(favs))
	}
}

func TestRemoveModelFavorite(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_ = db.AddModelFavorite("opencode", "anthropic", "claude-opus-4")
	if err := db.RemoveModelFavorite("opencode", "anthropic", "claude-opus-4"); err != nil {
		t.Fatalf("RemoveModelFavorite: %v", err)
	}

	favs, _ := db.ModelFavorites("opencode")
	if len(favs) != 0 {
		t.Errorf("expected 0 favorites after remove, got %d", len(favs))
	}
}

func TestRemoveModelFavorite_Nonexistent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// Removing a non-existent favorite is a no-op (no error).
	if err := db.RemoveModelFavorite("opencode", "anthropic", "claude-opus-4"); err != nil {
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

	_ = db.AddModelFavorite("opencode", "anthropic", "claude-opus-4")
	_ = db.AddModelFavorite("claude-code", "anthropic", "claude-opus-4")

	oc, _ := db.ModelFavorites("opencode")
	cc, _ := db.ModelFavorites("claude-code")
	if len(oc) != 1 || len(cc) != 1 {
		t.Fatalf("expected 1 favorite per platform, got opencode=%d, claude-code=%d", len(oc), len(cc))
	}

	// Unfavoriting one platform must leave the other alone.
	_ = db.RemoveModelFavorite("opencode", "anthropic", "claude-opus-4")
	oc, _ = db.ModelFavorites("opencode")
	cc, _ = db.ModelFavorites("claude-code")
	if len(oc) != 0 {
		t.Errorf("opencode favorites should be empty, got %d", len(oc))
	}
	if len(cc) != 1 {
		t.Errorf("claude-code favorite should survive, got %d", len(cc))
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

	favs, err := db.ModelFavorites("opencode")
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
