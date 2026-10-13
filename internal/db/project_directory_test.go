package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProjectDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	rw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// prd_wt has sessions in two sibling worktrees and the main checkout; the
	// sibling rows are inserted first so a LIMIT 1 over sessions would pick one.
	if _, err := rw.Exec(`CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL);
		CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT, directory TEXT);
		INSERT INTO project VALUES ('prd_wt','/src/repo'), ('global','/'), ('prd_idle','/src/idle');
		INSERT INTO session VALUES
			('s1','prd_wt','/src/.worktrees/repo/b'),
			('s2','prd_wt','/src/.worktrees/repo/a'),
			('s3','prd_wt','/src/repo'),
			('s4','global','/tmp/x'),
			('s5','prd_orphan','/src/orphan')`); err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for id, want := range map[string]string{
		"prd_wt":     "/src/repo", // sibling worktrees resolve to the one project root
		"global":     "",          // non-git project has no single directory
		"prd_idle":   "",          // no session: not a known project
		"prd_orphan": "",          // sessions without a project row: ambiguous, omitted
		"missing":    "",
		"":           "",
	} {
		for range 3 { // deterministic across repeated reads
			if got, err := d.ProjectDirectory(t.Context(), id); err != nil || got != want {
				t.Fatalf("ProjectDirectory(%q) = %q, %v; want %q", id, got, err, want)
			}
		}
	}
}

func TestProjectDirectoryWithoutProjectTableFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	rw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT, directory TEXT);
		INSERT INTO session VALUES ('s1','prd_1','/src/repo')`); err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if got, err := d.ProjectDirectory(t.Context(), "prd_1"); err == nil || got != "" {
		t.Fatalf("missing project table: %q, %v; want error and empty", got, err)
	}
}
