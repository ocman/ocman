package workflows

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBlobStorePutGetDedup(t *testing.T) {
	store := NewBlobStore(t.TempDir())
	payload := []byte("hello workflow artifact")

	hash, err := store.Put(payload)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if hash != Hash(payload) {
		t.Fatalf("put hash %q != Hash %q", hash, Hash(payload))
	}

	got, err := store.Get(hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("round-trip mismatch: %q", got)
	}

	// Identical bytes dedup to one file.
	hash2, err := store.Put(payload)
	if err != nil {
		t.Fatalf("second put: %v", err)
	}
	if hash2 != hash {
		t.Fatalf("dedup failed: %q != %q", hash2, hash)
	}
	count := 0
	root := store.root
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			count++
		}
		return nil
	})
	if count != 1 {
		t.Fatalf("expected exactly one blob file after dedup, got %d", count)
	}

	// Different bytes produce a different content address.
	other, err := store.Put([]byte("different"))
	if err != nil {
		t.Fatalf("put other: %v", err)
	}
	if other == hash {
		t.Fatalf("distinct payloads collided on hash %q", hash)
	}
}

func TestBlobStoreGetMissingReportsCleanup(t *testing.T) {
	store := NewBlobStore(t.TempDir())
	_, err := store.Get(Hash([]byte("never stored")))
	if !errors.Is(err, ErrPayloadMissing) {
		t.Fatalf("expected ErrPayloadMissing, got %v", err)
	}
}

func TestBlobStoreRemoveIsIdempotent(t *testing.T) {
	store := NewBlobStore(t.TempDir())
	hash, err := store.Put([]byte("payload"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !store.Has(hash) {
		t.Fatalf("expected Has to be true after put")
	}
	if err := store.Remove(hash); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if store.Has(hash) {
		t.Fatalf("expected Has to be false after remove")
	}
	// Removing again is a no-op.
	if err := store.Remove(hash); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if _, err := store.Get(hash); !errors.Is(err, ErrPayloadMissing) {
		t.Fatalf("expected ErrPayloadMissing after remove, got %v", err)
	}
}
