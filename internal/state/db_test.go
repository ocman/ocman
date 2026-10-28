package state

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/testutil"
	_ "modernc.org/sqlite"
)

func TestGetSettingCancelledContext(t *testing.T) {
	database := openTestStateDB(t)

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

// openTestStateDB opens a fresh state database with the schema migrated
// to the latest version.
func openTestStateDB(t testing.TB) *DB {
	t.Helper()
	stateDB, err := Open(templateDBPath(t))
	if err != nil {
		t.Fatalf("opening test state db: %v", err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })
	return stateDB
}

// templateDBPath is statetest.Path for this package's own tests, which
// cannot import statetest without an import cycle.
func templateDBPath(t testing.TB) string {
	t.Helper()
	return testutil.TemplateCopy(t, "state.db", filepath.Join(t.TempDir(), "state.db"), func(path string) error {
		db, err := Open(path)
		if err != nil {
			return err
		}
		return db.Close()
	})
}

// k is a tiny shorthand for constructing Key values in tests.
func k(platform, id string) Key { return Key{Platform: platform, SessionID: id} }

// sessionOp archives (updated > 0) or unarchives (updated == 0) a session.
type sessionOp struct {
	platform, id string
	updated      int64
}

func TestArchiveSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		ops  []sessionOp
		want map[Key]int64
	}{
		{"archive", []sessionOp{{"opencode", "s1", 1000}}, map[Key]int64{k("opencode", "s1"): 1000}},
		{"upsert", []sessionOp{{"opencode", "s1", 1000}, {"opencode", "s1", 2000}}, map[Key]int64{k("opencode", "s1"): 2000}},
		{"unarchive", []sessionOp{{"opencode", "s1", 1000}, {"opencode", "s1", 0}}, map[Key]int64{}},
		{"unarchive nonexistent", []sessionOp{{"opencode", "nonexistent", 0}}, map[Key]int64{}},
		// The same session ID is archived independently per platform, and
		// unarchiving one platform's entry leaves the other's alone.
		{"per-platform", []sessionOp{{"opencode", "s1", 1000}, {"other-platform", "s1", 2000}},
			map[Key]int64{k("opencode", "s1"): 1000, k("other-platform", "s1"): 2000}},
		{"per-platform unarchive", []sessionOp{{"opencode", "s1", 1000}, {"other-platform", "s1", 2000}, {"opencode", "s1", 0}},
			map[Key]int64{k("other-platform", "s1"): 2000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestStateDB(t)
			for _, op := range tc.ops {
				var err error
				if op.updated > 0 {
					err = db.ArchiveSession(t.Context(), op.platform, op.id, op.updated)
				} else {
					err = db.UnarchiveSession(t.Context(), op.platform, op.id)
				}
				if err != nil {
					t.Fatalf("%+v: %v", op, err)
				}
			}
			archived, err := db.ArchivedSessions(t.Context())
			if err != nil {
				t.Fatalf("ArchivedSessions: %v", err)
			}
			if !maps.Equal(archived, tc.want) {
				t.Errorf("archived = %v, want %v", archived, tc.want)
			}
		})
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

// TestModelFavorites_OrderedByCreatedAt checks that favorites come
// back in insertion order — the merger uses this to preserve the
// order the user starred them in.
func TestModelFavorites_OrderedByCreatedAt(t *testing.T) {
	db := openTestStateDB(t)

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

// favoriteOp adds (or removes) anthropic/claude-opus-4 on a platform.
type favoriteOp struct {
	add      bool
	platform string
}

func TestModelFavorites(t *testing.T) {
	const provider, model = "anthropic", "claude-opus-4"
	for _, tc := range []struct {
		name string
		ops  []favoriteOp
		want map[string]int // favorite count per platform
	}{
		{"add", []favoriteOp{{true, "opencode"}}, map[string]int{"opencode": 1}},
		{"add is idempotent", []favoriteOp{{true, "opencode"}, {true, "opencode"}, {true, "opencode"}}, map[string]int{"opencode": 1}},
		{"remove", []favoriteOp{{true, "opencode"}, {false, "opencode"}}, map[string]int{"opencode": 0}},
		{"remove nonexistent", []favoriteOp{{false, "opencode"}}, map[string]int{"opencode": 0}},
		// The same (provider, model) is favorited independently per platform.
		{"per-platform", []favoriteOp{{true, "opencode"}, {true, "other-platform"}},
			map[string]int{"opencode": 1, "other-platform": 1}},
		{"per-platform remove", []favoriteOp{{true, "opencode"}, {true, "other-platform"}, {false, "opencode"}},
			map[string]int{"opencode": 0, "other-platform": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestStateDB(t)
			for _, op := range tc.ops {
				var err error
				if op.add {
					err = db.AddModelFavorite(t.Context(), op.platform, provider, model)
				} else {
					err = db.RemoveModelFavorite(t.Context(), op.platform, provider, model)
				}
				if err != nil {
					t.Fatalf("%+v: %v", op, err)
				}
			}
			for platform, want := range tc.want {
				favs, err := db.ModelFavorites(t.Context(), platform)
				if err != nil {
					t.Fatalf("ModelFavorites(%s): %v", platform, err)
				}
				if len(favs) != want {
					t.Fatalf("%s favorites = %+v, want %d", platform, favs, want)
				}
				for _, f := range favs {
					if f.Platform != platform || f.Provider != provider || f.Model != model {
						t.Errorf("unexpected favorite: %+v", f)
					}
				}
			}
		})
	}
}

// --- Pinned session tests ---

func TestPinSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		ops  []sessionOp // updated > 0 pins, 0 unpins
		want []Key
	}{
		{"empty", nil, nil},
		{"pin", []sessionOp{{"opencode", "s1", 1}}, []Key{k("opencode", "s1")}},
		{"unpin", []sessionOp{{"opencode", "s1", 1}, {"opencode", "s1", 0}}, nil},
		{"unpin nonexistent", []sessionOp{{"opencode", "nonexistent", 0}}, nil},
		{"per-platform", []sessionOp{{"opencode", "s1", 1}, {"other-platform", "s1", 1}},
			[]Key{k("opencode", "s1"), k("other-platform", "s1")}},
		{"per-platform unpin", []sessionOp{{"opencode", "s1", 1}, {"other-platform", "s1", 1}, {"opencode", "s1", 0}},
			[]Key{k("other-platform", "s1")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestStateDB(t)
			for _, op := range tc.ops {
				var err error
				if op.updated > 0 {
					err = db.PinSession(t.Context(), op.platform, op.id)
				} else {
					err = db.UnpinSession(t.Context(), op.platform, op.id)
				}
				if err != nil {
					t.Fatalf("%+v: %v", op, err)
				}
			}
			pinned, err := db.PinnedSessions(t.Context())
			if err != nil {
				t.Fatalf("PinnedSessions: %v", err)
			}
			if len(pinned) != len(tc.want) {
				t.Fatalf("pinned = %v, want %v", pinned, tc.want)
			}
			for _, key := range tc.want {
				if pinned[key] <= 0 {
					t.Errorf("%v pinnedAt = %d, want positive", key, pinned[key])
				}
			}
		})
	}
}

func TestPinSession_Idempotent(t *testing.T) {
	db := openTestStateDB(t)

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

// --- Setting tests (schema v12) ---

func TestSetting(t *testing.T) {
	type kv struct{ key, val string }
	for _, tc := range []struct {
		name string
		sets []kv
		key  string
		want string
		ok   bool
	}{
		{"missing", nil, "pr_prompt_template", "", false},
		{"set then get", []kv{{"pr_prompt_template", "Handle PR #{number}"}}, "pr_prompt_template", "Handle PR #{number}", true},
		{"overwrite", []kv{{"issue_prompt_template", "first"}, {"issue_prompt_template", "second"}}, "issue_prompt_template", "second", true},
		{"independent keys a", []kv{{"a", "alpha"}, {"b", "beta"}}, "a", "alpha", true},
		{"independent keys b", []kv{{"a", "alpha"}, {"b", "beta"}}, "b", "beta", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestStateDB(t)
			for _, s := range tc.sets {
				if err := db.SetSetting(t.Context(), s.key, s.val); err != nil {
					t.Fatalf("SetSetting %s: %v", s.key, err)
				}
			}
			val, ok, err := db.GetSetting(t.Context(), tc.key)
			if err != nil {
				t.Fatalf("GetSetting: %v", err)
			}
			if val != tc.want || ok != tc.ok {
				t.Errorf("GetSetting(%s) = (%q, %v), want (%q, %v)", tc.key, val, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestWorktreeInheritPermissions(t *testing.T) {
	db := openTestStateDB(t)

	// An absent setting defaults to on; each write then round-trips.
	for _, set := range []*bool{nil, new(false), new(true)} {
		want := true
		if set != nil {
			want = *set
			if err := db.SetWorktreeInheritPermissions(t.Context(), want); err != nil {
				t.Fatalf("SetWorktreeInheritPermissions(%v): %v", want, err)
			}
		}
		on, err := db.GetWorktreeInheritPermissions(t.Context())
		if err != nil {
			t.Fatalf("GetWorktreeInheritPermissions: %v", err)
		}
		if on != want {
			t.Errorf("GetWorktreeInheritPermissions = %v, want %v", on, want)
		}
	}
}
