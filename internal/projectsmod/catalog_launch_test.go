package projectsmod

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestCatalogMatchesAPIAndMergeRepresentative(t *testing.T) {
	f := newFixture(t, db.ProjectStats{Directory: "/source", LastUsed: 2}, db.ProjectStats{Directory: "/target", LastUsed: 1})
	if rec := f.call(http.MethodPost, "/api/project/merge", `{"source":{"directory":"/source"},"target":{"directory":"/target"}}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	got, err := f.m.Catalog(t.Context())
	if err != nil || !reflect.DeepEqual(got, f.list(t)) || len(got) != 1 || got[0].Directory != "/target" {
		t.Fatalf("catalog: %+v, %v", got, err)
	}
	f.host.err = http.ErrServerClosed
	if _, err := f.m.Catalog(t.Context()); err == nil {
		t.Fatal("ignored catalog read failure")
	}
}
