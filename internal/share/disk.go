package share

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DiskStore is the relay's object storage, backed by a directory tree.
// Object keys map directly to relative paths under Root.
//
// The surface stays small: no directories, no rename, no append, no
// compare-and-swap and no server-allocated identifiers. Sequence numbers
// are allocated by the writer, so a relay never needs an atomic counter.
//
// Contract:
//
//   - Keys are segment-aligned: one or more segments of [A-Za-z0-9_-]
//     joined by "/". Anything else is rejected rather than sanitised, so
//     a traversal attempt fails loudly.
//   - Prefixes passed to List and DeletePrefix must end on a segment
//     boundary. Both accept a trailing "/" and treat it as equivalent.
//   - List of a prefix with no objects returns an empty slice and no
//     error. DeletePrefix of an absent prefix is a no-op.
//   - Put overwrites an existing key. Writers rely on this: a retried
//     append is a byte-identical overwrite.
type DiskStore struct {
	Root string
}

// OpenStore opens the disk store named by the relay's -store flag: a
// path, optionally prefixed with disk:// or file://.
func OpenStore(spec string) (*DiskStore, error) {
	if spec == "" {
		return nil, fmt.Errorf("share: empty store specification")
	}
	path := spec
	for _, scheme := range []string{"disk://", "file://"} {
		path = strings.TrimPrefix(path, scheme)
	}
	if scheme, _, ok := strings.Cut(path, "://"); ok {
		return nil, fmt.Errorf("share: unsupported store scheme %q", scheme)
	}
	return NewDiskStore(path)
}

// NewDiskStore returns a DiskStore rooted at dir, creating it if needed.
func NewDiskStore(dir string) (*DiskStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("share: disk store needs a root directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("share: resolving store root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("share: creating store root: %w", err)
	}
	return &DiskStore{Root: abs}, nil
}

// path resolves a validated key to an absolute filesystem path.
func (d *DiskStore) path(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return filepath.Join(d.Root, filepath.FromSlash(key)), nil
}

// Put writes an object, replacing any existing one. The write goes to a
// temporary file in the destination directory and is then renamed, so a
// reader never observes a partially written chunk.
func (d *DiskStore) Put(_ context.Context, key string, data []byte) error {
	full, err := d.path(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("share: creating object directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("share: creating temp object: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("share: writing object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("share: closing object: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("share: setting object mode: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return fmt.Errorf("share: publishing object: %w", err)
	}
	return nil
}

// Get reads an object, returning ErrNotFound when the key is absent.
func (d *DiskStore) Get(_ context.Context, key string) ([]byte, error) {
	full, err := d.path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("share: reading object: %w", err)
	}
	return b, nil
}

// List returns every object under a prefix. A prefix with no objects
// yields an empty slice, not an error.
func (d *DiskStore) List(_ context.Context, prefix string) ([]Object, error) {
	if err := ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	dir := d.Root
	if trimmed != "" {
		dir = filepath.Join(d.Root, filepath.FromSlash(trimmed))
	}

	out := []Object{}
	err := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		// Skip in-flight temporary files from a concurrent Put; they
		// are not yet objects and their names are not valid keys.
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			return nil
		}
		rel, err := filepath.Rel(d.Root, p)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		out = append(out, Object{Key: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("share: listing objects: %w", err)
	}
	return out, nil
}

// DeletePrefix removes every object under a prefix. An empty prefix is
// rejected so a bug cannot wipe the whole store in one call.
func (d *DiskStore) DeletePrefix(_ context.Context, prefix string) error {
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return fmt.Errorf("share: refusing to delete the entire store")
	}
	if err := ValidateKey(trimmed); err != nil {
		return err
	}
	full := filepath.Join(d.Root, filepath.FromSlash(trimmed))
	if err := os.RemoveAll(full); err != nil {
		return fmt.Errorf("share: deleting objects: %w", err)
	}
	return nil
}
