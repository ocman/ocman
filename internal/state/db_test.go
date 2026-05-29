package state

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

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
	if err := db.ArchiveSession("other-platform", "s1", 2000); err != nil {
		t.Fatalf("ArchiveSession other-platform: %v", err)
	}
	archived, err := db.ArchivedSessions()
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
	if archived[k("other-platform", "s1")] != 2000 {
		t.Error("other-platform/s1 should survive opencode unarchive")
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
	_ = db.AddModelFavorite("other-platform", "anthropic", "claude-opus-4")

	oc, _ := db.ModelFavorites("opencode")
	cc, _ := db.ModelFavorites("other-platform")
	if len(oc) != 1 || len(cc) != 1 {
		t.Fatalf("expected 1 favorite per platform, got opencode=%d, other-platform=%d", len(oc), len(cc))
	}

	// Unfavoriting one platform must leave the other alone.
	_ = db.RemoveModelFavorite("opencode", "anthropic", "claude-opus-4")
	oc, _ = db.ModelFavorites("opencode")
	cc, _ = db.ModelFavorites("other-platform")
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

// --- Pinned session tests ---

func TestPinSession_RoundTrip(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.PinSession("opencode", "s1"); err != nil {
		t.Fatalf("PinSession: %v", err)
	}

	pinned, err := db.PinnedSessions()
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

	if err := db.PinSession("opencode", "s1"); err != nil {
		t.Fatalf("PinSession: %v", err)
	}
	firstPinned, _ := db.PinnedSessions()
	firstAt := firstPinned[k("opencode", "s1")]

	// Pin again — should be a no-op (pinned_at unchanged).
	if err := db.PinSession("opencode", "s1"); err != nil {
		t.Fatalf("PinSession (repeat): %v", err)
	}
	secondPinned, _ := db.PinnedSessions()
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

	_ = db.PinSession("opencode", "s1")
	if err := db.UnpinSession("opencode", "s1"); err != nil {
		t.Fatalf("UnpinSession: %v", err)
	}

	pinned, _ := db.PinnedSessions()
	if len(pinned) != 0 {
		t.Errorf("expected 0 pinned sessions after unpin, got %d", len(pinned))
	}
}

func TestUnpinSession_Nonexistent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.UnpinSession("opencode", "nonexistent"); err != nil {
		t.Fatalf("UnpinSession on nonexistent: %v", err)
	}
}

func TestPinSession_PerPlatformIsolation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_ = db.PinSession("opencode", "s1")
	_ = db.PinSession("other-platform", "s1")

	pinned, _ := db.PinnedSessions()
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
	_ = db.UnpinSession("opencode", "s1")
	pinned, _ = db.PinnedSessions()
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

	pinned, err := db.PinnedSessions()
	if err != nil {
		t.Fatalf("PinnedSessions: %v", err)
	}
	if len(pinned) != 0 {
		t.Errorf("expected 0 pinned sessions on fresh db, got %d", len(pinned))
	}
}

// --- Child session tests ---

func makeChildSession(id, parentID string) ChildSession {
	return ChildSession{
		ID:              id,
		Platform:        "opencode",
		ParentSessionID: parentID,
		Intent:          "fix the linting issue",
		ComposedPrompt:  "## Task\nfix the linting issue\n",
		Status:          "starting",
		CreatedAt:       1000,
	}
}

func TestInsertChildSession_RoundTrip(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	cs := makeChildSession("child-1", "parent-1")
	cs.WorktreePath = "/tmp/worktrees/repo/fix-lint"
	cs.Branch = "fix-lint"
	cs.TmuxTarget = "~/src/repo:wt-fix-lint"

	if err := db.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	got, err := db.GetChildSession("child-1")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if got.ID != cs.ID {
		t.Errorf("ID: got %q, want %q", got.ID, cs.ID)
	}
	if got.ParentSessionID != cs.ParentSessionID {
		t.Errorf("ParentSessionID: got %q, want %q", got.ParentSessionID, cs.ParentSessionID)
	}
	if got.Intent != cs.Intent {
		t.Errorf("Intent: got %q, want %q", got.Intent, cs.Intent)
	}
	if got.WorktreePath != cs.WorktreePath {
		t.Errorf("WorktreePath: got %q, want %q", got.WorktreePath, cs.WorktreePath)
	}
	if got.Branch != cs.Branch {
		t.Errorf("Branch: got %q, want %q", got.Branch, cs.Branch)
	}
	if got.TmuxTarget != cs.TmuxTarget {
		t.Errorf("TmuxTarget: got %q, want %q", got.TmuxTarget, cs.TmuxTarget)
	}
	if got.Status != "starting" {
		t.Errorf("Status: got %q, want %q", got.Status, "starting")
	}
	if got.CompletedAt != 0 {
		t.Errorf("CompletedAt: expected 0, got %d", got.CompletedAt)
	}
}

func TestInsertChildSession_NullableFields(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// split_to_session: no worktree, no branch, no tmux target
	cs := makeChildSession("child-2", "parent-1")
	if err := db.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	got, err := db.GetChildSession("child-2")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if got.WorktreePath != "" {
		t.Errorf("WorktreePath: expected empty, got %q", got.WorktreePath)
	}
	if got.Branch != "" {
		t.Errorf("Branch: expected empty, got %q", got.Branch)
	}
	if got.TmuxTarget != "" {
		t.Errorf("TmuxTarget: expected empty, got %q", got.TmuxTarget)
	}
}

func TestUpdateChildSession_StatusTransition(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	cs := makeChildSession("child-3", "parent-1")
	if err := db.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	// Transition to running
	if err := db.UpdateChildSession("child-3", "running", "", 0); err != nil {
		t.Fatalf("UpdateChildSession running: %v", err)
	}
	got, _ := db.GetChildSession("child-3")
	if got.Status != "running" {
		t.Errorf("expected status=running, got %q", got.Status)
	}
	if got.CompletedAt != 0 {
		t.Errorf("expected completedAt=0, got %d", got.CompletedAt)
	}

	// Transition to completed with summary
	if err := db.UpdateChildSession("child-3", "completed", "Fixed 3 lint errors.", 9999); err != nil {
		t.Fatalf("UpdateChildSession completed: %v", err)
	}
	got, _ = db.GetChildSession("child-3")
	if got.Status != "completed" {
		t.Errorf("expected status=completed, got %q", got.Status)
	}
	if got.Summary != "Fixed 3 lint errors." {
		t.Errorf("expected summary, got %q", got.Summary)
	}
	if got.CompletedAt != 9999 {
		t.Errorf("expected completedAt=9999, got %d", got.CompletedAt)
	}
}

func TestListChildSessionsByParent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	// Two children for parent-1, one for parent-2
	cs1 := makeChildSession("child-a", "parent-1")
	cs1.CreatedAt = 100
	cs2 := makeChildSession("child-b", "parent-1")
	cs2.CreatedAt = 200
	cs3 := makeChildSession("child-c", "parent-2")
	cs3.CreatedAt = 300

	for _, cs := range []ChildSession{cs1, cs2, cs3} {
		if err := db.InsertChildSession(cs); err != nil {
			t.Fatalf("InsertChildSession %s: %v", cs.ID, err)
		}
	}

	children, err := db.ListChildSessionsByParent("parent-1")
	if err != nil {
		t.Fatalf("ListChildSessionsByParent: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 children for parent-1, got %d", len(children))
	}
	// Ordered by created_at DESC: child-b first
	if children[0].ID != "child-b" {
		t.Errorf("expected child-b first (newest), got %q", children[0].ID)
	}
	if children[1].ID != "child-a" {
		t.Errorf("expected child-a second, got %q", children[1].ID)
	}

	// parent-2 has exactly one child
	children2, _ := db.ListChildSessionsByParent("parent-2")
	if len(children2) != 1 || children2[0].ID != "child-c" {
		t.Errorf("parent-2: expected [child-c], got %v", children2)
	}

	// Unknown parent returns empty slice
	children3, _ := db.ListChildSessionsByParent("no-such-parent")
	if len(children3) != 0 {
		t.Errorf("expected empty for unknown parent, got %d", len(children3))
	}
}

func TestListNonTerminalChildSessions(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	cs1 := makeChildSession("child-x", "parent-1")
	cs1.Status = "starting"
	cs2 := makeChildSession("child-y", "parent-1")
	cs2.Status = "running"
	cs3 := makeChildSession("child-z", "parent-1")
	cs3.Status = "completed"

	for _, cs := range []ChildSession{cs1, cs2, cs3} {
		if err := db.InsertChildSession(cs); err != nil {
			t.Fatalf("InsertChildSession %s: %v", cs.ID, err)
		}
	}

	active, err := db.ListNonTerminalChildSessions()
	if err != nil {
		t.Fatalf("ListNonTerminalChildSessions: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 non-terminal sessions, got %d", len(active))
	}
	ids := map[string]bool{active[0].ID: true, active[1].ID: true}
	if !ids["child-x"] || !ids["child-y"] {
		t.Errorf("expected child-x and child-y, got %v", ids)
	}
}

func TestCancelChildSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	cs := makeChildSession("child-cancel", "parent-1")
	if err := db.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	if err := db.CancelChildSession("child-cancel", 5000); err != nil {
		t.Fatalf("CancelChildSession: %v", err)
	}

	got, _ := db.GetChildSession("child-cancel")
	if got.Status != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", got.Status)
	}
	if got.CompletedAt != 5000 {
		t.Errorf("expected completedAt=5000, got %d", got.CompletedAt)
	}
}

func TestCancelChildSession_Idempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	cs := makeChildSession("child-idem", "parent-1")
	cs.Status = "completed"
	if err := db.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	// Cancelling a completed session is a no-op
	if err := db.CancelChildSession("child-idem", 9999); err != nil {
		t.Fatalf("CancelChildSession on completed: %v", err)
	}
	got, _ := db.GetChildSession("child-idem")
	if got.Status != "completed" {
		t.Errorf("expected status to remain completed, got %q", got.Status)
	}
}

func TestGetChildSession_NotFound(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_, err := db.GetChildSession("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent child session")
	}
}

// --- Setting tests (schema v12) ---

func TestSetting_GetMissingReturnsOkFalse(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	val, ok, err := db.GetSetting("pr_prompt_template")
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

	if err := db.SetSetting("pr_prompt_template", "Handle PR #{number}"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	val, ok, err := db.GetSetting("pr_prompt_template")
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

	if err := db.SetSetting("issue_prompt_template", "first"); err != nil {
		t.Fatalf("SetSetting first: %v", err)
	}
	if err := db.SetSetting("issue_prompt_template", "second"); err != nil {
		t.Fatalf("SetSetting second: %v", err)
	}

	val, ok, err := db.GetSetting("issue_prompt_template")
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

	if err := db.SetSetting("a", "alpha"); err != nil {
		t.Fatalf("SetSetting a: %v", err)
	}
	if err := db.SetSetting("b", "beta"); err != nil {
		t.Fatalf("SetSetting b: %v", err)
	}

	a, _, err := db.GetSetting("a")
	if err != nil {
		t.Fatalf("GetSetting a: %v", err)
	}
	b, _, err := db.GetSetting("b")
	if err != nil {
		t.Fatalf("GetSetting b: %v", err)
	}
	if a != "alpha" || b != "beta" {
		t.Errorf("expected (alpha, beta), got (%q, %q)", a, b)
	}
}
