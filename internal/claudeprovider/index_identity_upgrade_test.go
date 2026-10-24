package claudeprovider

import (
	"path/filepath"
	"testing"
)

func TestIndexUpgradeReparsesUnchangedMisclassifiedTranscripts(t *testing.T) {
	config := fixture(t)
	path := filepath.Join(t.TempDir(), "index.db")
	x, err := OpenIndex(path, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Version 3 could mistake a configuration ancestor for a native parent.
	// Keep transcript mtimes and sizes intact to exercise the warm-cache skip.
	if _, err := x.db.Exec(`UPDATE session SET root_id='wrong-root', parent_id='wrong-root' WHERE id=?`, mainID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	x, err = OpenIndex(path, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = x.Close() })
	if err := x.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	root, err := x.get(t.Context(), mainID)
	if err != nil || root == nil || root.RootID != mainID || root.ParentID != "" {
		t.Fatalf("warm index kept incorrect root identity: %+v %v", root, err)
	}
	child, err := x.get(t.Context(), childID)
	if err != nil || child == nil || child.RootID != mainID || child.ParentID != mainID {
		t.Fatalf("rebuilt index lost child hierarchy: %+v %v", child, err)
	}
}
