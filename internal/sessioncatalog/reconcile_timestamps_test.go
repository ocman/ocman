package sessioncatalog

import (
	"fmt"
	"testing"
)

func TestReconcileImportCreationTimestamps(t *testing.T) {
	const now int64 = 1792497600123
	for _, tc := range []struct {
		name   string
		native int64
		want   int64
	}{
		{"historical milliseconds", 1790841600456, 1790841600456},
		{"positive minimum", 1, 1},
		{"future clock skew", now + 60000, now + 60000},
		{"unknown", 0, now},
		{"negative", -1, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openStore(t)
			tx, err := s.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			row, created, deferred, err := reconcileRow(t.Context(), tx, catalogSource(), NativeSession{ID: "historical", CreatedAt: tc.native}, now)
			if err != nil || !created || deferred {
				t.Fatalf("import: %+v created=%v deferred=%v err=%v", row, created, deferred, err)
			}
			stored, err := get(t.Context(), tx, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.CreatedAt != tc.want || stored.UpdatedAt != now {
				t.Fatalf("created_at=%d, want %d; updated_at=%d, want catalogue time %d", stored.CreatedAt, tc.want, stored.UpdatedAt, now)
			}
			// A metadata change advances catalogue modification time, never creation.
			if _, _, _, err := reconcileRow(t.Context(), tx, catalogSource(), NativeSession{ID: "historical", CreatedAt: tc.native + 1000, Title: "renamed"}, now+2000); err != nil {
				t.Fatal(err)
			}
			stored, err = get(t.Context(), tx, row.ID)
			if err != nil || stored.CreatedAt != tc.want || stored.UpdatedAt != now+2000 {
				t.Fatalf("metadata update changed timestamp semantics: %+v %v", stored, err)
			}
			if _, _, _, err := reconcileRow(t.Context(), tx, catalogSource(), NativeSession{ID: "historical", CreatedAt: tc.native + 5000, Title: "renamed"}, now+3000); err != nil {
				t.Fatal(err)
			}
			unchanged, err := get(t.Context(), tx, row.ID)
			if err != nil || unchanged != stored {
				t.Fatalf("native timestamp alone changed catalogue metadata: %+v %v", unchanged, err)
			}
		})
	}
}

func TestReconcileDoesNotRepairExistingCreationTimestamps(t *testing.T) {
	for _, size := range []int{1, 32} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, _ := openStore(t)
			metadata := make([]NativeSession, size)
			for i := range metadata {
				metadata[i] = NativeSession{ID: fmt.Sprintf("native-%d", i)}
			}
			if _, err := s.Reconcile(t.Context(), catalogSource(), metadata); err != nil {
				t.Fatal(err)
			}
			before, err := s.FindBinding(t.Context(), nativeBinding(metadata[0].ID))
			if err != nil {
				t.Fatal(err)
			}
			for i := range metadata {
				metadata[i].CreatedAt = 1790841600456
			}
			for _, rename := range []bool{false, true} {
				if rename {
					metadata[0].Title = "changed metadata"
				}
				result, err := s.Reconcile(t.Context(), catalogSource(), metadata)
				if err != nil || result.Created != 0 {
					t.Fatalf("rediscovery: %+v %v", result, err)
				}
				after, err := s.Get(t.Context(), before.ID)
				if err != nil || after.CreatedAt != before.CreatedAt || (!rename && after.UpdatedAt != before.UpdatedAt) {
					t.Fatalf("rediscovery repaired timestamps: before=%+v after=%+v err=%v", before, after, err)
				}
			}
		})
	}
}

func TestReconcilePreservesReservedCreationTimestamp(t *testing.T) {
	s, _ := openStore(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: binding().NativeID, CreatedAt: 1790841600456}}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(t.Context(), reserved.ID)
	if err != nil || after.CreatedAt != reserved.CreatedAt {
		t.Fatalf("native discovery changed reserved creation time: %+v %v", after, err)
	}
}

func TestReconcileMissingParentCreationTimestampStaysUnknown(t *testing.T) {
	s, _ := openStore(t)
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "child", ParentID: "parent", CreatedAt: 1790841600456}}); err != nil {
		t.Fatal(err)
	}
	parent, err := s.FindBinding(t.Context(), nativeBinding("parent"))
	if err != nil || parent.CreatedAt <= 0 || parent.CreatedAt != parent.UpdatedAt {
		t.Fatalf("placeholder should use import time: %+v %v", parent, err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "parent", CreatedAt: 1790755200456}}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(t.Context(), parent.ID)
	if err != nil || after.CreatedAt != parent.CreatedAt {
		t.Fatalf("late metadata repaired existing placeholder timestamp: %+v %v", after, err)
	}
}
