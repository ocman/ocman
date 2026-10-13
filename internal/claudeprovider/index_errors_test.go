package claudeprovider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndexSyncPreservesRowsWhenDirectoryCannotBeRead(t *testing.T) {
	for _, level := range []string{"projects", "project", "subagents"} {
		t.Run(level, func(t *testing.T) {
			config := t.TempDir()
			projects := filepath.Join(config, "projects")
			project := filepath.Join(projects, "repo")
			subagents := filepath.Join(project, "session", "subagents")
			if err := os.MkdirAll(subagents, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(project, "session.jsonl"), filepath.Join(subagents, "agent-child.jsonl")} {
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			x, err := OpenIndex(filepath.Join(t.TempDir(), "index.db"), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = x.Close() })
			if err := x.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := map[string]string{"projects": projects, "project": project, "subagents": subagents}[level]
			if err := os.Rename(path, path+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := x.Sync(t.Context()); err == nil {
				t.Fatal("directory read failure was treated as a successful sync")
			}
			var count int
			if err := x.db.QueryRow(`SELECT count(*) FROM session`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("indexed sessions = %d, err = %v; want both preserved", count, err)
			}
		})
	}
}

func TestScanFileReturnsReadErrors(t *testing.T) {
	if e, err := scanFile(t.TempDir()); err == nil || e != nil {
		t.Fatalf("directory read: entry=%+v err=%v; want a read error", e, err)
	}
}
