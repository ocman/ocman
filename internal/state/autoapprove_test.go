package state

import (
	"os"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAutoApprove_DefaultAbsent(t *testing.T) {
	db := openTestDB(t)
	enabled, exists, err := db.GetAutoApprove("opencode", "sess-1")
	if err != nil {
		t.Fatalf("GetAutoApprove: %v", err)
	}
	if exists {
		t.Error("expected no row to exist for fresh session")
	}
	if enabled {
		t.Error("expected enabled=false when row absent")
	}
}

func TestAutoApprove_SetAndGet(t *testing.T) {
	db := openTestDB(t)

	if err := db.SetAutoApprove("opencode", "sess-1", true); err != nil {
		t.Fatalf("SetAutoApprove(true): %v", err)
	}
	enabled, exists, err := db.GetAutoApprove("opencode", "sess-1")
	if err != nil {
		t.Fatalf("GetAutoApprove: %v", err)
	}
	if !exists {
		t.Fatal("expected row to exist after SetAutoApprove")
	}
	if !enabled {
		t.Error("expected enabled=true")
	}
}

func TestAutoApprove_Disable(t *testing.T) {
	db := openTestDB(t)

	_ = db.SetAutoApprove("opencode", "sess-1", true)
	if err := db.SetAutoApprove("opencode", "sess-1", false); err != nil {
		t.Fatalf("SetAutoApprove(false): %v", err)
	}
	enabled, exists, err := db.GetAutoApprove("opencode", "sess-1")
	if err != nil {
		t.Fatalf("GetAutoApprove: %v", err)
	}
	if !exists {
		t.Fatal("expected row to still exist after disable")
	}
	if enabled {
		t.Error("expected enabled=false after disable")
	}
}

func TestAutoApprove_PlatformScoped(t *testing.T) {
	db := openTestDB(t)

	_ = db.SetAutoApprove("opencode", "sess-1", true)

	// Different platform, same session ID — must be independent.
	enabled, exists, err := db.GetAutoApprove("other-platform", "sess-1")
	if err != nil {
		t.Fatalf("GetAutoApprove other-platform: %v", err)
	}
	if exists {
		t.Error("expected no row for other-platform")
	}
	if enabled {
		t.Error("expected enabled=false for other-platform")
	}
}

// Ensure the test binary can find os.TempDir even when the tmp dir is
// a different path — just a guard that the test helper works.
var _ = os.TempDir
