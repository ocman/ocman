package share_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/share"
)

func newTestStore(t *testing.T) *share.DiskStore {
	t.Helper()
	s, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	return s
}

// TestDiskStore_Contract exercises the contract documented on DiskStore.
func TestDiskStore_Contract(t *testing.T) {
	t.Run("get returns what put wrote", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if err := s.Put(ctx, "20260813/abc/meta", []byte("hello")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := s.Get(ctx, "20260813/abc/meta")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if string(got) != "hello" {
			t.Fatalf("got %q want %q", got, "hello")
		}
	})

	t.Run("get of a missing key returns ErrNotFound", func(t *testing.T) {
		s := newTestStore(t)
		_, err := s.Get(context.Background(), "20260813/abc/nope")
		if !errors.Is(err, share.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	})

	// Writers rely on overwrite for idempotent retries: re-uploading a
	// chunk must replace it, not fail or duplicate.
	t.Run("put overwrites an existing key", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if err := s.Put(ctx, "d/id/000000001", []byte("first")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Put(ctx, "d/id/000000001", []byte("second")); err != nil {
			t.Fatalf("Put overwrite: %v", err)
		}
		got, err := s.Get(ctx, "d/id/000000001")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if string(got) != "second" {
			t.Fatalf("got %q want %q", got, "second")
		}
		objs, err := s.List(ctx, "d/id/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(objs) != 1 {
			t.Fatalf("overwrite produced %d objects, want 1", len(objs))
		}
	})

	t.Run("put stores empty objects", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if err := s.Put(ctx, "d/id/empty", nil); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := s.Get(ctx, "d/id/empty")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d bytes, want 0", len(got))
		}
	})

	t.Run("list reports full keys and sizes", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if err := s.Put(ctx, "20260813/abc/000000001", []byte("1234")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Put(ctx, "20260813/abc/000000002", []byte("123456")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		objs, err := s.List(ctx, "20260813/abc/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(objs) != 2 {
			t.Fatalf("got %d objects, want 2", len(objs))
		}
		sizes := map[string]int64{}
		for _, o := range objs {
			sizes[o.Key] = o.Size
		}
		if sizes["20260813/abc/000000001"] != 4 {
			t.Fatalf("sizes = %v; key must be the full key and size the byte length", sizes)
		}
		if sizes["20260813/abc/000000002"] != 6 {
			t.Fatalf("sizes = %v", sizes)
		}
	})

	t.Run("list accepts a prefix with and without a trailing slash", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if err := s.Put(ctx, "20260813/abc/meta", []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		withSlash, err := s.List(ctx, "20260813/abc/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		without, err := s.List(ctx, "20260813/abc")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(withSlash) != 1 || len(without) != 1 {
			t.Fatalf("trailing slash changed the result: %d vs %d", len(withSlash), len(without))
		}
	})

	t.Run("list is scoped to the prefix", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if err := s.Put(ctx, "20260813/abc/meta", []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Put(ctx, "20260813/def/meta", []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		objs, err := s.List(ctx, "20260813/abc/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(objs) != 1 || objs[0].Key != "20260813/abc/meta" {
			t.Fatalf("prefix leaked neighbouring keys: %v", objs)
		}
	})

	t.Run("list of an absent prefix is empty and not an error", func(t *testing.T) {
		s := newTestStore(t)
		objs, err := s.List(context.Background(), "19700101/gone/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(objs) != 0 {
			t.Fatalf("got %d objects, want 0", len(objs))
		}
	})

	t.Run("delete prefix removes everything beneath it", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		for _, k := range []string{"20260813/abc/meta", "20260813/abc/000000001", "20260813/def/meta"} {
			if err := s.Put(ctx, k, []byte("x")); err != nil {
				t.Fatalf("Put %s: %v", k, err)
			}
		}
		if err := s.DeletePrefix(ctx, "20260813/abc/"); err != nil {
			t.Fatalf("DeletePrefix: %v", err)
		}
		objs, err := s.List(ctx, "20260813/abc/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(objs) != 0 {
			t.Fatalf("delete left %d objects", len(objs))
		}
		if _, err := s.Get(ctx, "20260813/def/meta"); err != nil {
			t.Fatalf("delete removed a neighbouring prefix: %v", err)
		}
	})

	t.Run("delete of an absent prefix is a no-op", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.DeletePrefix(context.Background(), "19700101/gone/"); err != nil {
			t.Fatalf("DeletePrefix: %v", err)
		}
	})

	t.Run("delete refuses an empty prefix", func(t *testing.T) {
		s := newTestStore(t)
		for _, p := range []string{"", "/"} {
			if err := s.DeletePrefix(context.Background(), p); err == nil {
				t.Fatalf("DeletePrefix(%q) succeeded; it would wipe the store", p)
			}
		}
	})

	// Share ids reach the store straight from a URL path, so a backend
	// must reject traversal rather than sanitise it.
	t.Run("traversal and unsafe keys are rejected", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		bad := []string{
			"../escape",
			"20260813/../../etc/passwd",
			"20260813/./abc",
			"/absolute",
			"20260813//abc",
			"",
			"has space",
			"has\x00null",
		}
		for _, k := range bad {
			if err := s.Put(ctx, k, []byte("x")); err == nil {
				t.Errorf("Put(%q) succeeded, want rejection", k)
			}
			if _, err := s.Get(ctx, k); err == nil {
				t.Errorf("Get(%q) succeeded, want rejection", k)
			}
		}
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

func TestOpenStore(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{name: "bare path", spec: root},
		{name: "disk scheme", spec: "disk://" + root},
		{name: "file scheme", spec: "file://" + root},
		{name: "s3 unsupported", spec: "s3://bucket/prefix", wantErr: "unsupported"},
		{name: "unknown scheme", spec: "gcs://bucket", wantErr: "unsupported"},
		{name: "empty", spec: "", wantErr: "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := share.OpenStore(tc.spec)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("OpenStore(%q) succeeded, want error containing %q", tc.spec, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("OpenStore(%q): %v", tc.spec, err)
			}
			want, _ := filepath.Abs(root)
			if s.Root != want {
				t.Fatalf("root = %q, want %q", s.Root, want)
			}
		})
	}
}

// TestOpenStore_RelativeDiskScheme covers the relay's default -store value.
func TestOpenStore_RelativeDiskScheme(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := share.OpenStore("disk://./relay-data")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	want, _ := filepath.Abs("relay-data")
	if s.Root != want {
		t.Fatalf("root = %q, want %q", s.Root, want)
	}
}

func TestOpenStore_DecodesSchemePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "relay data")
	s, err := share.OpenStore("disk://" + strings.ReplaceAll(root, " ", "%20"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if s.Root != root {
		t.Fatalf("root = %q, want %q", s.Root, root)
	}
}
