package owned

import "testing"

// Git-command events read their session hint from the stored row: directory
// and project come from statev2, and native caller IDs are rejected.
func TestCachedSessionHintComesFromStoredMetadata(t *testing.T) {
	a, p, id := storedFixture(t)
	p.rows = nil // the native source no longer lists it
	row, ok := a.CachedSession(id)
	if !ok || row.ID != id || row.Directory != "/stored" || row.Title != "stored" {
		t.Fatalf("stored hint: %+v %v", row, ok)
	}
	if _, ok := a.CachedSession("native"); ok {
		t.Fatal("native caller ID accepted")
	}
	if _, ok := a.CachedSession("ocm_00000000-0000-0000-0000-000000000000"); ok {
		t.Fatal("unknown identity produced a hint")
	}
}
