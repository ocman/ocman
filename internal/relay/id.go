package relay

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// idRandomBytes is the entropy behind a share id. 16 bytes = 128 bits,
// enough that ids are unguessable on their own; the content is
// additionally protected by the key in the URL fragment, which the relay
// never sees.
const idRandomBytes = 16

// dateLayout is the date partition prefix. Partitioning by creation date
// makes expiry a prefix delete instead of a metadata scan, and maps
// cleanly onto an object store's lifecycle rules.
const dateLayout = "20060102"

// seqDigits is the fixed width of a chunk's key segment, so chunk keys
// sort lexicographically in the same order as numerically.
const seqDigits = 9

// newID returns a share id of the form "YYYYMMDD-<random>". The date is
// part of the id so a reader's URL alone determines the storage prefix,
// with no index to consult.
func newID(now time.Time) (string, error) {
	b := make([]byte, idRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("relay: generating share id: %w", err)
	}
	return now.UTC().Format(dateLayout) + "-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// splitID separates a share id into its date and random components,
// validating both. Ids arrive straight from a URL path, so this is the
// boundary that keeps untrusted input out of storage keys.
func splitID(id string) (date, random string, ok bool) {
	date, random, found := strings.Cut(id, "-")
	if !found || len(date) != len(dateLayout) || random == "" {
		return "", "", false
	}
	if _, err := time.Parse(dateLayout, date); err != nil {
		return "", "", false
	}
	if len(random) != base64.RawURLEncoding.EncodedLen(idRandomBytes) {
		return "", "", false
	}
	if _, err := base64.RawURLEncoding.DecodeString(random); err != nil {
		return "", "", false
	}
	return date, random, true
}

// validID reports whether an id is well formed.
func validID(id string) bool {
	_, _, ok := splitID(id)
	return ok
}

// prefixFor returns the storage prefix holding a share's objects. The
// date and random parts become separate key segments so neither the
// share id nor its separator ever appears inside one segment.
func prefixFor(id string) (string, bool) {
	date, random, ok := splitID(id)
	if !ok {
		return "", false
	}
	return date + "/" + random + "/", true
}

// datePrefix returns the storage prefix for every share created on a
// given day.
func datePrefix(day time.Time) string {
	return day.UTC().Format(dateLayout) + "/"
}

// metaKey returns the key of a share's metadata object.
func metaKey(id string) (string, bool) {
	prefix, ok := prefixFor(id)
	if !ok {
		return "", false
	}
	return prefix + "meta", true
}

// chunkKey returns the key of one chunk.
func chunkKey(id string, seq uint64) (string, bool) {
	prefix, ok := prefixFor(id)
	if !ok {
		return "", false
	}
	return prefix + fmt.Sprintf("%0*d", seqDigits, seq), true
}

// seqFromKey extracts a sequence number from a chunk key, reporting
// false for anything that is not a chunk (notably the meta object).
func seqFromKey(key string) (uint64, bool) {
	idx := strings.LastIndexByte(key, '/')
	if idx < 0 {
		return 0, false
	}
	name := key[idx+1:]
	if len(name) != seqDigits {
		return 0, false
	}
	seq, err := strconv.ParseUint(name, 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}
