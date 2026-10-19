package statev2

import "testing"

func TestSizeBytesIncludesCommittedWALPagesAndReportsClosedStore(t *testing.T) {
	d, err := Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	before, err := d.SizeBytes(t.Context())
	if err != nil || before <= 0 {
		t.Fatalf("initial size %d: %v", before, err)
	}
	if _, err := d.db.Exec(`CREATE TABLE size_probe(data BLOB); INSERT INTO size_probe VALUES (zeroblob(1048576))`); err != nil {
		t.Fatal(err)
	}
	after, err := d.SizeBytes(t.Context())
	if err != nil || after < before+1048576 {
		t.Fatalf("WAL pages not counted: %d -> %d, %v", before, after, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SizeBytes(t.Context()); err == nil {
		t.Fatal("closed store returned a size")
	}
}
