package claudeprovider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIndexFilesystemEventDoesNotScanOtherProjects(t *testing.T) {
	config := fixture(t)
	x := openSynced(t, config)
	paths := []string{
		filepath.Join(config, "projects", "-work-other", otherID+".jsonl"),
		filepath.Join(config, "projects", "-work-repo", mainID+".jsonl"),
	}
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, st.ModTime().Add(1), st.ModTime().Add(1)); err != nil {
			t.Fatal(err)
		}
	}
	before := x.parsed.Load()
	if err := x.syncChanges(t.Context(), []string{filepath.Dir(paths[0])}); err != nil {
		t.Fatal(err)
	}
	if n := x.parsed.Load() - before; n != 1 {
		t.Fatalf("one project's directory event reparsed %d files, want 1", n)
	}
}

func TestIndexDirectoryDiscoveryDeletionAndOverflow(t *testing.T) {
	config := fixture(t)
	x := openSynced(t, config)
	path := filepath.Join(config, "projects", "-new-project", "new.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"user\",\"uuid\":\"new-prompt\",\"message\":{\"content\":\"hello\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := x.syncChanges(t.Context(), []string{filepath.Dir(path), t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if e, err := x.get(t.Context(), "new"); err != nil || e == nil {
		t.Fatalf("new transcript not discovered: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := x.syncChanges(t.Context(), []string{filepath.Dir(path)}); err != nil {
		t.Fatal(err)
	}
	if e, err := x.get(t.Context(), "new"); err != nil || e != nil {
		t.Fatalf("deleted transcript retained in native index: %v", err)
	}
	if err := x.syncChanges(t.Context(), []string{""}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := x.syncChanges(ctx, []string{config}); err == nil {
		t.Fatal("cancelled refresh succeeded")
	}
	if DefaultOptions(t.TempDir()).Sweep != 5*time.Minute {
		t.Fatal("default sweep is not a recovery-only interval")
	}
}

func TestDirectoryWatcherReportsChangedPath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native watchers unavailable")
	}
	changes := make(chan string, 32)
	w, err := newDirWatcher(func(path string) {
		select {
		case changes <- path:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	dir := t.TempDir()
	w.Add(dir)
	w.Add(dir)
	path := filepath.Join(dir, "new.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-changes:
		if got != dir && got != path {
			t.Fatalf("watch returned unrelated path %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not report a directory change")
	}
}

func TestSidecarEventRefreshesOnlyItsTranscriptAndIndexesParentLookups(t *testing.T) {
	config := fixture(t)
	x := openSynced(t, config)
	child, err := x.get(t.Context(), childID)
	if err != nil {
		t.Fatal(err)
	}
	meta := strings.TrimSuffix(child.Path, ".jsonl") + ".meta.json"
	if err := os.WriteFile(meta, []byte(`{"toolUseId":"updated-tool"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := x.parsed.Load()
	if err := x.syncChanges(t.Context(), []string{meta}); err != nil {
		t.Fatal(err)
	}
	child, err = x.get(t.Context(), childID)
	if err != nil || child.ParentToolUseID != "updated-tool" || child.ParentID != mainID || x.parsed.Load()-before != 1 {
		t.Fatalf("sidecar refresh: %+v %v reparses=%d", child, err, x.parsed.Load()-before)
	}
	var id, parent, unused int
	var plan string
	if err := x.db.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM session WHERE parent_id=?`, mainID).Scan(&id, &parent, &unused, &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "session_parent") {
		t.Fatalf("child lookup scans unrelated sessions: %s", plan)
	}
}
