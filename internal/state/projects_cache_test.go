package state

import (
	"bytes"
	"testing"
	"time"
)

func TestProjectsCacheRoundTrip(t *testing.T) {
	db, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	data, refreshedAt, err := db.ProjectsCache(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if data != nil || !refreshedAt.IsZero() {
		t.Fatalf("empty cache = %q, %v", data, refreshedAt)
	}

	wantData := []byte(`[{"directory":"/repo"}]`)
	wantTime := time.UnixMilli(1234)
	if err := db.SaveProjectsCache(t.Context(), wantData, wantTime); err != nil {
		t.Fatal(err)
	}

	data, refreshedAt, err = db.ProjectsCache(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, wantData) || !refreshedAt.Equal(wantTime) {
		t.Fatalf("cache = %q, %v; want %q, %v", data, refreshedAt, wantData, wantTime)
	}
}
