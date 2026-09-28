package main

import (
	"path/filepath"
	"testing"
)

func TestOpenOpenCodeDB_MissingFileIsNotFatal(t *testing.T) {
	database, issue, err := openOpenCodeDB(filepath.Join(t.TempDir(), "opencode.db"))
	if err != nil || database != nil {
		t.Fatalf("want nil db and nil error, got db=%v err=%v", database, err)
	}
	if issue == nil || issue.ID != "opencode-db-missing" {
		t.Fatalf("want opencode-db-missing issue, got %+v", issue)
	}
}

func TestOpenOpenCodeDB_UnreadableIsFatal(t *testing.T) {
	// A directory exists but is not a database: the error must surface.
	database, issue, err := openOpenCodeDB(t.TempDir())
	if database != nil {
		database.Close()
	}
	if err == nil || issue != nil {
		t.Fatalf("want error and no issue, got issue=%+v err=%v", issue, err)
	}
}
