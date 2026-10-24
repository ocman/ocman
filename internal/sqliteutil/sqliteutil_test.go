package sqliteutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}

func TestSecurePaths_CreatesPrivateDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "state.db")
	if err := SecurePaths(dbPath, "state"); err != nil {
		t.Fatalf("SecurePaths: %v", err)
	}
	assertFileMode(t, filepath.Dir(dbPath), 0o700)
	assertFileMode(t, dbPath, 0o600)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(dbPath + suffix); !os.IsNotExist(err) {
			t.Errorf("%s sidecar created: %v", suffix, err)
		}
	}
}

func TestSecurePaths_RepairsSidecars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "state.db")
	if err := SecurePaths(dbPath, "state"); err != nil {
		t.Fatalf("SecurePaths: %v", err)
	}
	if err := os.Chmod(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := dbPath + suffix
		if err := os.WriteFile(path, nil, 0o666); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	if err := SecurePaths(dbPath, "state"); err != nil {
		t.Fatalf("repair sidecars: %v", err)
	}
	assertFileMode(t, filepath.Dir(dbPath), 0o700)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		assertFileMode(t, dbPath+suffix, 0o600)
	}
}

func TestSecurePaths_RejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	err := SecurePaths(filepath.Join(linkDir, "x.db"), "history")
	if err == nil || !strings.Contains(err.Error(), "history directory") || !strings.Contains(err.Error(), "must be a real directory") {
		t.Fatalf("symlinked directory error = %v", err)
	}

	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(realDir, "x.db")
	if err := os.Symlink(target, dbPath+"-wal"); err != nil {
		t.Fatal(err)
	}
	err = SecurePaths(dbPath, "history")
	if err == nil || !strings.Contains(err.Error(), "must be a regular file") {
		t.Fatalf("symlinked sidecar error = %v", err)
	}
	assertFileMode(t, target, 0o644)
}

func TestSecurePaths_DirectoryCreationFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SecurePaths(filepath.Join(file, "sub", "x.db"), "state"); err == nil || !strings.Contains(err.Error(), "creating state directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenPool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	conn, err := OpenPool("file:"+path, "test", Pool{MaxOpen: 3, MaxIdle: 2, MaxLifetime: time.Minute, MaxIdleTime: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.Stats().MaxOpenConnections; got != 3 {
		t.Errorf("MaxOpenConnections = %d, want 3", got)
	}
	var one int
	if err := conn.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("query = %d, %v", one, err)
	}

	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := OpenPool("file:"+missing+"?mode=ro", "test", Pool{MaxOpen: 1}); err == nil {
		t.Fatal("ping of a missing read-only database succeeded")
	}
}
