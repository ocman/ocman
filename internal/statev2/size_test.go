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
	// The probe may reuse pages that migrations freed.
	var free int64
	if err := d.db.QueryRow(`SELECT freelist_count*page_size FROM pragma_freelist_count, pragma_page_size`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`CREATE TABLE size_probe(data BLOB); INSERT INTO size_probe VALUES (zeroblob(1048576))`); err != nil {
		t.Fatal(err)
	}
	after, err := d.SizeBytes(t.Context())
	if err != nil || after < before+1048576-free {
		t.Fatalf("WAL pages not counted: %d -> %d, %v", before, after, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SizeBytes(t.Context()); err == nil {
		t.Fatal("closed store returned a size")
	}
}
