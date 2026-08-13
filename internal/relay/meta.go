package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/share"
)

// metaVersion is the schema version of the stored metadata object.
const metaVersion = 1

// deleteTokenBytes is the entropy behind a share's delete token.
// 32 bytes = 256 bits, matching the strength of ocman's other
// credentials.
const deleteTokenBytes = 32

// meta is the per-share metadata object. It is the only bookkeeping the
// relay keeps, which is what lets it run without a database.
//
// The delete token is stored hashed, never in the clear: reading the
// storage backend must not hand over the right to append to or revoke
// somebody's share.
type meta struct {
	Version    int    `json:"v"`
	DeleteHash string `json:"deleteHash"`
	CreatedAt  int64  `json:"createdAt"`
}

// newDeleteToken returns a fresh delete token and its stored hash.
func newDeleteToken() (token, hash string, err error) {
	b := make([]byte, deleteTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("relay: generating delete token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

// hashToken returns the hex-encoded SHA-256 of a delete token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// authorises reports whether a presented token matches the stored hash.
// The comparison is constant time.
func (m meta) authorises(token string) bool {
	if token == "" || m.DeleteHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(m.DeleteHash)) == 1
}

// putMeta writes a share's metadata object.
func putMeta(ctx context.Context, store share.Store, id string, m meta) error {
	key, ok := metaKey(id)
	if !ok {
		return fmt.Errorf("relay: invalid share id")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("relay: encoding metadata: %w", err)
	}
	return store.Put(ctx, key, b)
}

// getMeta reads a share's metadata object. It reports false when the
// share does not exist; callers must not distinguish that from an
// authorisation failure.
func getMeta(ctx context.Context, store share.Store, id string) (meta, bool, error) {
	key, ok := metaKey(id)
	if !ok {
		return meta{}, false, nil
	}
	b, err := store.Get(ctx, key)
	if errors.Is(err, share.ErrNotFound) {
		return meta{}, false, nil
	}
	if err != nil {
		return meta{}, false, fmt.Errorf("relay: reading metadata: %w", err)
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return meta{}, false, fmt.Errorf("relay: decoding metadata: %w", err)
	}
	return m, true, nil
}
