package opencode

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionMetadataNeverReadsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Deliberately omit message and part tables. Any transcript/defaults or
	// aggregate-summary read fails, while an indexed metadata read works.
	_, err = raw.Exec(`CREATE TABLE session (
		id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, title TEXT,
		directory TEXT, time_created INTEGER, time_updated INTEGER,
		summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, share_url TEXT
	); INSERT INTO session VALUES ('child', 'project', 'parent', 'Owner child', '/repo', 1, 2, 0, 0, 0, '')`)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	a := New(database, nil)
	row, err := a.SessionMetadata(t.Context(), "child")
	if err != nil || row == nil || row.Title != "Owner child" || row.ParentID != "parent" {
		t.Fatalf("metadata=%+v error=%v", row, err)
	}
	if _, err := a.SessionMetadata(t.Context(), "missing"); err == nil {
		t.Fatal("missing session succeeded")
	}
	if _, err := (&Adapter{}).SessionMetadata(t.Context(), "child"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("nil database error=%v", err)
	}
}
