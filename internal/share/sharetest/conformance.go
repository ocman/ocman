// Package sharetest provides a conformance suite every share.Store
// implementation must pass.
//
// It exists so a second backend (an object store) can be verified
// against the same contract as the filesystem backend without
// re-deriving what that contract is. Add a case here whenever the
// contract in share.Store changes, not to one backend's own tests.
package sharetest

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/share"
)

// Factory builds a fresh, empty Store for one subtest.
type Factory func(t *testing.T) share.Store

// RunStoreConformance exercises the full share.Store contract.
func RunStoreConformance(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("get returns what put wrote", func(t *testing.T) {
		s := newStore(t)
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
		s := newStore(t)
		_, err := s.Get(context.Background(), "20260813/abc/nope")
		if !errors.Is(err, share.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	})

	// Writers rely on overwrite for idempotent retries: re-uploading a
	// chunk must replace it, not fail or duplicate.
	t.Run("put overwrites an existing key", func(t *testing.T) {
		s := newStore(t)
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
		s := newStore(t)
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
		s := newStore(t)
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
		s := newStore(t)
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
		s := newStore(t)
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
		s := newStore(t)
		objs, err := s.List(context.Background(), "19700101/gone/")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(objs) != 0 {
			t.Fatalf("got %d objects, want 0", len(objs))
		}
	})

	t.Run("delete prefix removes everything beneath it", func(t *testing.T) {
		s := newStore(t)
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
		s := newStore(t)
		if err := s.DeletePrefix(context.Background(), "19700101/gone/"); err != nil {
			t.Fatalf("DeletePrefix: %v", err)
		}
	})

	t.Run("delete refuses an empty prefix", func(t *testing.T) {
		s := newStore(t)
		for _, p := range []string{"", "/"} {
			if err := s.DeletePrefix(context.Background(), p); err == nil {
				t.Fatalf("DeletePrefix(%q) succeeded; it would wipe the store", p)
			}
		}
	})

	// Share ids reach the store straight from a URL path, so a backend
	// must reject traversal rather than sanitise it.
	t.Run("traversal and unsafe keys are rejected", func(t *testing.T) {
		s := newStore(t)
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
