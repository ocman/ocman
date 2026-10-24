package share

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned by DiskStore.Get for a key that does not exist.
var ErrNotFound = errors.New("share: object not found")

// Object is a single stored object as reported by DiskStore.List.
type Object struct {
	// Key is the full object key, not a path relative to the listed
	// prefix.
	Key string
	// Size is the stored size in bytes.
	Size int64
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

// ValidateKey checks a full object key against the store contract.
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

// ValidatePrefix checks a prefix against the store contract. An empty
// prefix means "everything" and is permitted for List but rejected by
// DeletePrefix, so a bug cannot wipe an entire bucket in one call.
func ValidatePrefix(prefix string) error {
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return nil
	}
	return ValidateKey(trimmed)
}
