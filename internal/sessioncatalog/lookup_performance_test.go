package sessioncatalog

import (
	"fmt"
	"strings"
	"testing"
)

func TestBindingLookupUsesExactPhysicalSourceIndex(t *testing.T) {
	s, _ := openStore(t)
	b := nativeBinding("native")
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+bindingQuery, b.OwnerID, b.Platform, b.SourceKey, b.NativeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "session_catalog_binding") {
		t.Fatalf("binding lookup scans other sessions instead of exact physical identity: %s", plan)
	}
}

func TestLegacyBindingLookupUsesNativeIDIndex(t *testing.T) {
	s, _ := openStore(t)
	b := nativeBinding("native")
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+legacyBindingQuery, b.OwnerID, b.Platform, b.NativeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "session_catalog_legacy_binding") {
		t.Fatalf("legacy association lookup scans an owner's catalogue: %s", plan)
	}
}

func BenchmarkReconcileInventory(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, _ := openStore(b)
			rows := make([]NativeSession, count)
			for i := range rows {
				rows[i] = NativeSession{ID: fmt.Sprint(i), Directory: "/repo"}
				if i > 0 {
					rows[i].ParentID = "0"
				}
			}
			if _, err := s.Reconcile(b.Context(), catalogSource(), rows); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for b.Loop() {
				if _, err := s.Reconcile(b.Context(), catalogSource(), rows); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
