package claudeprovider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnershipReloadsDespiteMatchingFileMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := os.WriteFile(path, []byte(`{"first":{"directory":"/one"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &live{opts: Options{StatePath: path}}
	l.syncOwnedLocked()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"first":{"directory":"/one"},"second":{"directory":"/two"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	l.syncOwnedLocked()
	if l.owned["second"] == nil {
		t.Fatal("metadata cache hid newly shared ownership")
	}
}
