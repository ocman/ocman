package claudeprovider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
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

func TestOwnershipBatchOverlaysIndexedAndPendingSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := os.WriteFile(path, []byte(`{
		"indexed":{"directory":"/work","title":"Renamed"},
		"pending":{"directory":"/work","title":"Pending"},
		"outside":{"directory":"/other"},
		"foreign":{"directory":"/work","config":"/other-config"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &live{opts: Options{StatePath: path, ConfigDir: "/config"}, status: map[string]db.Session{
		"indexed": {Status: db.StatusBusy}, "pending": {Status: db.StatusError},
	}}
	indexed := map[string]bool{"indexed": true, "terminal": true}
	sessions := l.overlayAndPending([]db.Session{{ID: "indexed", Title: "Original"}, {ID: "terminal"}}, indexed, "/work")
	if len(sessions) != 3 || sessions[0].Title != "Renamed" || !sessions[0].LiveConnection || sessions[0].Status != db.StatusBusy {
		t.Fatalf("indexed overlay: %+v", sessions)
	}
	if sessions[1].LiveConnection || sessions[2].ID != "pending" || !sessions[2].LiveConnection || sessions[2].Status != db.StatusError {
		t.Fatalf("pending overlay: %+v", sessions)
	}
	if pending := l.pending(indexed, "/work"); len(pending) != 1 || pending[0].ID != "pending" || pending[0].Status != db.StatusError {
		t.Fatalf("standalone pending: %+v", pending)
	}
}
