package share_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/share/sharetest"
)

func TestDiskStore_Conformance(t *testing.T) {
	sharetest.RunStoreConformance(t, func(t *testing.T) share.Store {
		t.Helper()
		s, err := share.NewDiskStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewDiskStore: %v", err)
		}
		return s
	})
}

func TestNewDiskStore_RejectsEmptyRoot(t *testing.T) {
	if _, err := share.NewDiskStore(""); err == nil {
		t.Fatal("NewDiskStore(\"\") succeeded, want error")
	}
}

func TestNewDiskStore_CreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "data")
	if _, err := share.NewDiskStore(root); err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("root not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("root is not a directory")
	}
}

// TestDiskStore_TraversalStaysInsideRoot is the belt-and-braces check
// behind key validation: even if a traversal key slipped through, no
// file may appear outside the root.
func TestDiskStore_TraversalStaysInsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "store")
	s, err := share.NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	_ = s.Put(context.Background(), "../escaped", []byte("pwned"))
	if _, err := os.Stat(filepath.Join(base, "escaped")); !os.IsNotExist(err) {
		t.Fatal("a write escaped the store root")
	}
}

// TestDiskStore_ListIgnoresInFlightTempFiles proves a concurrent Put's
// temporary file is never reported as an object; it would fail key
// validation on a later Get and corrupt chunk accounting.
func TestDiskStore_ListIgnoresInFlightTempFiles(t *testing.T) {
	root := t.TempDir()
	s, err := share.NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, "20260813/abc/000000001", []byte("real")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	tmp := filepath.Join(root, "20260813", "abc", ".tmp-inflight")
	if err := os.WriteFile(tmp, []byte("partial"), 0o600); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	objs, err := s.List(ctx, "20260813/abc/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 || objs[0].Key != "20260813/abc/000000001" {
		t.Fatalf("temp file leaked into List: %v", objs)
	}
}

func TestDiskStore_PutIsAtomic(t *testing.T) {
	root := t.TempDir()
	s, err := share.NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, "d/id/k", []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// No temp files may survive a successful Put.
	entries, err := os.ReadDir(filepath.Join(root, "d", "id"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Put left %d entries behind, want 1", len(entries))
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		ok   bool
	}{
		{"simple", "meta", true},
		{"nested", "20260813/abc/000000001", true},
		{"underscores and dashes", "a_b-c/d-e_f", true},
		{"empty", "", false},
		{"dot segment", "a/./b", false},
		{"parent segment", "a/../b", false},
		{"leading slash", "/a", false},
		{"trailing slash", "a/", false},
		{"double slash", "a//b", false},
		{"space", "a b", false},
		{"dot in name", "a.bin", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := share.ValidateKey(tc.key)
			if tc.ok && err != nil {
				t.Fatalf("ValidateKey(%q) = %v, want nil", tc.key, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ValidateKey(%q) = nil, want error", tc.key)
			}
		})
	}
}

func TestValidatePrefix_AllowsEmpty(t *testing.T) {
	if err := share.ValidatePrefix(""); err != nil {
		t.Fatalf("ValidatePrefix(\"\") = %v, want nil (empty means everything for List)", err)
	}
	if err := share.ValidatePrefix("a/b/"); err != nil {
		t.Fatalf("ValidatePrefix(\"a/b/\") = %v, want nil", err)
	}
	if err := share.ValidatePrefix("../x"); err == nil {
		t.Fatal("ValidatePrefix(\"../x\") = nil, want error")
	}
}
