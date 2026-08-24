package state

import (
	"os"
	"path/filepath"
	"reflect"
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
	enabled, exists, err := db.GetAutoApprove(t.Context(), "opencode", "sess-1")
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

	if err := db.SetAutoApprove(t.Context(), "opencode", "sess-1", true); err != nil {
		t.Fatalf("SetAutoApprove(true): %v", err)
	}
	enabled, exists, err := db.GetAutoApprove(t.Context(), "opencode", "sess-1")
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

	_ = db.SetAutoApprove(t.Context(), "opencode", "sess-1", true)
	if err := db.SetAutoApprove(t.Context(), "opencode", "sess-1", false); err != nil {
		t.Fatalf("SetAutoApprove(false): %v", err)
	}
	enabled, exists, err := db.GetAutoApprove(t.Context(), "opencode", "sess-1")
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

	_ = db.SetAutoApprove(t.Context(), "opencode", "sess-1", true)

	// Different platform, same session ID — must be independent.
	enabled, exists, err := db.GetAutoApprove(t.Context(), "other-platform", "sess-1")
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

func TestApprovedPermissionRoundTrip(t *testing.T) {
	db := openTestDB(t)
	want := ApprovedPermission{
		PermissionID: "p1", PermissionText: "bash", Patterns: []string{"git *"},
		ApprovedBy: "ai", Reply: "once", Metadata: map[string]any{"command": "git status", "timeout": float64(10)},
		AskedAt: 100, Reasoning: "safe", ApprovedAt: 200,
	}
	if err := db.RecordApprovedPermission(t.Context(), "opencode", "s1", want); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListApprovedPermissions(t.Context(), "opencode", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestApprovedPermissionActorIsNotDerivedFromReasoning(t *testing.T) {
	db := openTestDB(t)
	p := ApprovedPermission{PermissionID: "p1", PermissionText: "bash", Reasoning: "user clicked Allow always"}
	if err := db.RecordApprovedPermission(t.Context(), "opencode", "s1", p); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListApprovedPermissions(t.Context(), "opencode", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ApprovedBy != "ai" || rows[0].Reply != "once" || rows[0].UserApproved() {
		t.Fatal("AI approval was spoofed as user approval through reasoning")
	}
}

func TestRecordApprovedPermissionRejectsInvalidProvenance(t *testing.T) {
	db := openTestDB(t)
	for _, p := range []ApprovedPermission{
		{ApprovedBy: "admin", Reply: "once"},
		{ApprovedBy: "user", Reply: "sometimes"},
	} {
		if err := db.RecordApprovedPermission(t.Context(), "opencode", "s1", p); err == nil {
			t.Fatalf("accepted invalid provenance: %#v", p)
		}
	}
}

// Ensure the test binary can find os.TempDir even when the tmp dir is
// a different path — just a guard that the test helper works.
var _ = os.TempDir
