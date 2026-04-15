package state

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// openTestStateDB creates an in-memory state database with the schema initialized.
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

	if err := db.ArchiveSession("s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	archived, err := db.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived session, got %d", len(archived))
	}
	if archived["s1"] != 1000 {
		t.Errorf("expected timeUpdated=1000, got %d", archived["s1"])
	}
}

func TestArchiveSession_Upsert(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession("s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	// Update with newer timestamp
	if err := db.ArchiveSession("s1", 2000); err != nil {
		t.Fatalf("ArchiveSession (upsert): %v", err)
	}

	archived, err := db.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if archived["s1"] != 2000 {
		t.Errorf("expected timeUpdated=2000 after upsert, got %d", archived["s1"])
	}
}

func TestUnarchiveSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession("s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := db.UnarchiveSession("s1"); err != nil {
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

	// Should not error when unarchiving a session that was never archived
	if err := db.UnarchiveSession("nonexistent"); err != nil {
		t.Fatalf("UnarchiveSession on nonexistent: %v", err)
	}
}

// --- Seen tests ---

func TestMarkSessionSeen(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen("s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}

	seen, err := db.SeenSessions()
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected 1 seen session, got %d", len(seen))
	}
	if seen["s1"] != 1000 {
		t.Errorf("expected timeUpdated=1000, got %d", seen["s1"])
	}
}

func TestMarkSessionSeen_OnlyUpdatesIfNewer(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen("s1", 2000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}
	// Mark with an older timestamp — should NOT downgrade
	if err := db.MarkSessionSeen("s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen (older): %v", err)
	}

	seen, err := db.SeenSessions()
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if seen["s1"] != 2000 {
		t.Errorf("expected timeUpdated=2000 (not downgraded), got %d", seen["s1"])
	}
}

func TestMarkSessionSeen_UpdatesIfNewer(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.MarkSessionSeen("s1", 1000); err != nil {
		t.Fatalf("MarkSessionSeen: %v", err)
	}
	// Mark with a newer timestamp — should upgrade
	if err := db.MarkSessionSeen("s1", 3000); err != nil {
		t.Fatalf("MarkSessionSeen (newer): %v", err)
	}

	seen, err := db.SeenSessions()
	if err != nil {
		t.Fatalf("SeenSessions: %v", err)
	}
	if seen["s1"] != 3000 {
		t.Errorf("expected timeUpdated=3000 (upgraded), got %d", seen["s1"])
	}
}

func TestMultipleSessions(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.ArchiveSession("s1", 100); err != nil {
		t.Fatalf("ArchiveSession s1: %v", err)
	}
	if err := db.ArchiveSession("s2", 200); err != nil {
		t.Fatalf("ArchiveSession s2: %v", err)
	}
	if err := db.MarkSessionSeen("s3", 300); err != nil {
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

	// Verify the file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("database file was not created")
	}

	// Verify we can write
	if err := stateDB.ArchiveSession("test", 123); err != nil {
		t.Fatalf("ArchiveSession after Open: %v", err)
	}
}

func TestDefaultDBPath(t *testing.T) {
	path := DefaultDBPath()
	if path == "" {
		t.Fatal("DefaultDBPath returned empty string")
	}
	// Should end with the expected filename
	if filepath.Base(path) != "state.db" {
		t.Errorf("expected path ending in state.db, got %s", path)
	}
}
