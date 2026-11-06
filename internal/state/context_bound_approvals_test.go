package state

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestContextBoundApprovalMigrationAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateTo(raw, 128); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO auto_approved_permission(platform,session_id,permission_id,permission_text,patterns_json,judge_session_id,reasoning,approved_at) VALUES ('opencode','session','legacy','bash','["git status"]','','safe',1)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy, ok, err := db.ApprovedPermission(t.Context(), "opencode", "session", "legacy")
	if err != nil || !ok || legacy.ContextBound {
		t.Fatalf("legacy approval changed: %+v %v", legacy, err)
	}
	row := ApprovedPermission{PermissionID: "contextual", PermissionText: "bash", Patterns: []string{"git push"}, ContextBound: true, Metadata: map[string]any{"command": "git push origin topic"}}
	if err := db.RecordApprovedPermission(t.Context(), "opencode", "session", row); err != nil {
		t.Fatal(err)
	}
	// An ordinary replay must not turn a context-bound audit row into a grant.
	row.ContextBound = false
	if err := db.RecordApprovedPermission(t.Context(), "opencode", "session", row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ApprovedPermission(t.Context(), "opencode", "session", "contextual")
	if err != nil || !ok || !got.ContextBound || got.Metadata["command"] != row.Metadata["command"] {
		t.Fatalf("lost audit scope: %+v %v", got, err)
	}
}

func TestContextBoundMigrationToleratesMissingLegacyTable(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "partial.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateContextBoundApprovals(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := migrateContextBoundApprovals(tx); err == nil {
		t.Fatal("closed transaction accepted")
	}
}
