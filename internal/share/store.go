package share

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned by Store.Get for a key that does not exist.
var ErrNotFound = errors.New("share: object not found")

// Object is a single stored object as reported by Store.List.
type Object struct {
	// Key is the full object key, not a path relative to the listed
	// prefix.
	Key string
	// Size is the stored size in bytes.
	Size int64
}

// Store is the object storage behind a relay. It is deliberately the
// smallest surface that both a filesystem and an object store can
// implement natively: no directories, no rename, no append, no
// compare-and-swap, and no server-allocated identifiers.
//
// Sequence numbers are allocated by the writer, not the store, so a
// relay never needs an atomic counter and multiple relay processes can
// share one bucket without coordinating.
//
// Contract:
//
//   - Keys are segment-aligned: one or more segments of [A-Za-z0-9_-]
//     joined by "/". Implementations must reject anything else rather
//     than sanitising it, so a traversal attempt fails loudly.
//   - Prefixes passed to List and DeletePrefix must end on a segment
//     boundary. Both accept a trailing "/" and treat it as equivalent.
//   - List of a prefix with no objects returns an empty slice and no
//     error. DeletePrefix of an absent prefix is a no-op.
//   - Put overwrites an existing key. Writers rely on this: a retried
//     append is a byte-identical overwrite.
type Store interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]Object, error)
	DeletePrefix(ctx context.Context, prefix string) error
}

// validKeySegment reports whether one path segment is safe. The
// allowed set excludes "." so "." and ".." can never appear, which is
// what keeps a URL-supplied share id from escaping the storage root.
func validKeySegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ValidateKey checks a full object key against the Store contract.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("share: empty key")
	}
	for _, seg := range strings.Split(key, "/") {
		if !validKeySegment(seg) {
			return fmt.Errorf("share: invalid key %q", key)
		}
	}
	return nil
}

// ValidatePrefix checks a prefix against the Store contract. An empty
// prefix means "everything" and is permitted for List but rejected by
// DeletePrefix, so a bug cannot wipe an entire bucket in one call.
func ValidatePrefix(prefix string) error {
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return nil
	}
	return ValidateKey(trimmed)
}
