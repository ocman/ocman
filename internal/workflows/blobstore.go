package workflows

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrPayloadMissing is returned when an artifact's content-addressed
// payload has been removed by retention cleanup (metadata survives).
var ErrPayloadMissing = errors.New("artifact payload has been cleaned up")

// BlobStore is a content-addressed store for large workflow artifact
// payloads kept out of SQLite. Identical bytes share one file (dedup),
// so repeated payloads are referenced without extra copies.
type BlobStore struct {
	root string
}

// NewBlobStore roots a content store under dir (created lazily on the
// first write). dir is typically <ocman-data>/workflow-artifacts.
func NewBlobStore(dir string) *BlobStore {
	return &BlobStore{root: dir}
}

// Hash returns the content address (sha256 hex) of the given payload
// without writing it. Deterministic: identical bytes hash identically.
func Hash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// pathFor returns the on-disk path for a content hash, sharded by the
// first two hex chars to avoid a single huge directory.
func (b *BlobStore) pathFor(hash string) string {
	return filepath.Join(b.root, hash[:2], hash)
}

// Put writes payload to the content store and returns its hash. Writing
// the same bytes twice is a no-op on the second call (dedup): the file
// is left as-is. The write is atomic (temp file + rename) so a crash
// never leaves a partial blob at the content address.
func (b *BlobStore) Put(payload []byte) (string, error) {
	hash := Hash(payload)
	dest := b.pathFor(hash)
	if _, err := os.Stat(dest); err == nil {
		return hash, nil
	}
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating artifact directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("creating artifact temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("writing artifact payload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("closing artifact temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		// A concurrent writer may have won the race; if the blob now
		// exists the content is identical, so treat that as success.
		if _, statErr := os.Stat(dest); statErr == nil {
			return hash, nil
		}
		return "", fmt.Errorf("finalizing artifact payload: %w", err)
	}
	return hash, nil
}

// Get returns the payload for a content hash. Returns ErrPayloadMissing
// (wrapped) when the payload has been removed by cleanup.
func (b *BlobStore) Get(hash string) ([]byte, error) {
	payload, err := os.ReadFile(b.pathFor(hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", hash, ErrPayloadMissing)
	}
	if err != nil {
		return nil, fmt.Errorf("reading artifact payload: %w", err)
	}
	return payload, nil
}

// Has reports whether the payload for a content hash is still present.
func (b *BlobStore) Has(hash string) bool {
	_, err := os.Stat(b.pathFor(hash))
	return err == nil
}

// Remove deletes the payload for a content hash. Missing payloads are
// not an error (idempotent): cleanup can be retried safely.
func (b *BlobStore) Remove(hash string) error {
	err := os.Remove(b.pathFor(hash))
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("removing artifact payload: %w", err)
}
