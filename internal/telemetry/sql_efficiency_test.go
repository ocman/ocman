package telemetry

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/XSAM/otelsql"
	"modernc.org/sqlite"
)

func TestSQLDisabledBypassesInstrumentation(t *testing.T) {
	previous := sqlInstrumentationDisabled.Load()
	t.Cleanup(func() { sqlInstrumentationDisabled.Store(previous) })
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if _, err := Init(t.Context(), "", "test"); err != nil {
		t.Fatal(err)
	}
	db, err := OpenSQL(":memory:", "disabled")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, ok := db.Driver().(*sqlite.Driver); !ok {
		t.Fatalf("disabled telemetry wraps every SQL operation with %T", db.Driver())
	}
	if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
}

func TestSQLAttributeCacheBoundsAndConcurrentReads(t *testing.T) {
	var cache sqlAttributeCache
	for i := range sqlAttributeCacheSize + 10 {
		cache.get(fmt.Sprintf("SELECT column%d FROM session", i))
	}
	if len(cache.entries) != sqlAttributeCacheSize {
		t.Fatalf("cache size = %d", len(cache.entries))
	}
	if _, ok := cache.entries["SELECT column0 FROM session"]; ok {
		t.Fatal("oldest entry was not evicted")
	}
	long := "SELECT " + strings.Repeat("column,", sqlAttributeMaxQuery)
	if len(cache.get(long)) != 2 || len(cache.entries) != sqlAttributeCacheSize {
		t.Fatal("oversized query was not attributed without caching")
	}
	if _, ok := cache.entries[long]; ok {
		t.Fatal("oversized query retained")
	}
	if cache.get("") != nil || cache.get("-- only a comment") != nil {
		t.Fatal("empty SQL has query attributes")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				got := cache.get("SELECT id FROM session WHERE id=?")
				want := buildSQLQueryAttributes("SELECT id FROM session WHERE id=?")
				if !slices.Equal(got, want) {
					t.Error("concurrent cache read changed attributes")
				}
			}
		})
	}
	wg.Wait()
}

func TestSQLRepeatedQueryAttributesAvoidAllocations(t *testing.T) {
	query := "SELECT id FROM session WHERE id=?"
	sqlQueryAttributes(t.Context(), otelsql.MethodConnQuery, query, nil)
	allocs := testing.AllocsPerRun(100, func() {
		sqlQueryAttributes(t.Context(), otelsql.MethodConnQuery, query, nil)
	})
	if allocs > 0 {
		t.Fatalf("repeated query attributes allocate %.0f objects, want zero", allocs)
	}
}
