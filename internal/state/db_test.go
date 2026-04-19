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
