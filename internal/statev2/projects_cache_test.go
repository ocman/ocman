package statev2

import (
	"testing"
	"time"
)

func TestProjectsCacheIsOwnerScopedAndDisposable(t *testing.T) {
	d := openTestDB(t)
	if data, at, err := d.ProjectsCache(t.Context(), "owner"); err != nil || data != nil || !at.IsZero() {
		t.Fatalf("empty cache: %s %v %v", data, at, err)
	}
	at := time.UnixMilli(1234)
	for _, data := range []string{`[{"directory":"/old"}]`, `[{"directory":"/new"}]`} {
		if err := d.SaveProjectsCache(t.Context(), "owner", []byte(data), at); err != nil {
			t.Fatal(err)
		}
		got, refreshed, err := d.ProjectsCache(t.Context(), "owner")
		if err != nil || string(got) != data || !refreshed.Equal(at) {
			t.Fatalf("cache: %s %v %v", got, refreshed, err)
		}
	}
	if data, _, err := d.ProjectsCache(t.Context(), "another-owner"); err != nil || data != nil {
		t.Fatalf("cache leaked across owners: %s %v", data, err)
	}
	if err := d.SaveProjectsCache(t.Context(), "", []byte(`[]`), at); err == nil {
		t.Fatal("blank owner accepted")
	}
	_ = d.Close()
	if _, _, err := d.ProjectsCache(t.Context(), "owner"); err == nil {
		t.Fatal("closed store read accepted")
	}
	if err := d.SaveProjectsCache(t.Context(), "owner", []byte(`[]`), at); err == nil {
		t.Fatal("closed store write accepted")
	}
}
