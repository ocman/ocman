package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSizeBytesIncludesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	writer, err := OpenReadWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.db.Exec(`PRAGMA wal_autocheckpoint=0; CREATE TABLE payload (data BLOB); INSERT INTO payload VALUES (zeroblob(100000))`); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	size, err := reader.SizeBytes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if size < 100000 || size <= info.Size() {
		t.Fatalf("size %d does not include committed WAL pages, main file %d", size, info.Size())
	}
}
