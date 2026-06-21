package state

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// InstanceID is this ocman's stable random identifier. It is a short
// base32 string used to namespace remote sessions (r-<instanceID>:...)
// and to route commands to the correct host (AD-2, AD-5).
//
// RemoteToken is the secret a hub must present to dial this instance's
// gRPC server (AD-4). It is longer than the instance ID because, unlike
// the ID, it is a credential.
type InstanceIdentity struct {
	InstanceID  string
	RemoteToken string
	CreatedAt   int64
}

// instanceIDBytes is the number of random bytes behind the instance ID.
// 10 bytes base32-encodes to 16 chars (no padding) — plenty of entropy
// to keep ~10 instances collision-free (A-3) while staying short enough
// to read in a URL/badge.
const instanceIDBytes = 10

// remoteTokenBytes is the number of random bytes behind the remote-access
// token. 32 bytes = 256 bits, matching the share-token strength.
const remoteTokenBytes = 32

// instanceIDEncoding is lowercase, unpadded base32 so the ID is
// URL-safe and reads cleanly inside a compound platform key.
var instanceIDEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// generateInstanceID returns a cryptographically random instance ID.
func generateInstanceID() (string, error) {
	b := make([]byte, instanceIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating instance id: %w", err)
	}
	return instanceIDEncoding.EncodeToString(b), nil
}

// generateRemoteToken returns a cryptographically random access token.
func generateRemoteToken() (string, error) {
	b := make([]byte, remoteTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating remote token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// InstanceIdentity returns this ocman's persisted instance identity,
// generating and storing one on first call. Idempotent: subsequent
// calls return the same ID and token (FR-2, FR-4).
func (d *DB) InstanceIdentity() (InstanceIdentity, error) {
	var ident InstanceIdentity
	err := d.db.QueryRow(
		`SELECT instance_id, remote_token, created_at FROM instance_identity WHERE id = 1`,
	).Scan(&ident.InstanceID, &ident.RemoteToken, &ident.CreatedAt)
	if err == nil {
		return ident, nil
	}
	if err != sql.ErrNoRows {
		return InstanceIdentity{}, fmt.Errorf("reading instance identity: %w", err)
	}

	id, err := generateInstanceID()
	if err != nil {
		return InstanceIdentity{}, err
	}
	token, err := generateRemoteToken()
	if err != nil {
		return InstanceIdentity{}, err
	}
	now := time.Now().UnixMilli()
	// INSERT OR IGNORE so a concurrent caller that won the race keeps
	// its value; we then re-read whatever is stored.
	if _, err := d.db.Exec(
		`INSERT OR IGNORE INTO instance_identity (id, instance_id, remote_token, created_at)
		 VALUES (1, ?, ?, ?)`,
		id, token, now,
	); err != nil {
		return InstanceIdentity{}, fmt.Errorf("writing instance identity: %w", err)
	}
	if err := d.db.QueryRow(
		`SELECT instance_id, remote_token, created_at FROM instance_identity WHERE id = 1`,
	).Scan(&ident.InstanceID, &ident.RemoteToken, &ident.CreatedAt); err != nil {
		return InstanceIdentity{}, fmt.Errorf("re-reading instance identity: %w", err)
	}
	return ident, nil
}

// maskToken returns a display-safe rendering of a secret: the first and
// last few characters with the middle elided. Empty input yields "".
func maskToken(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return strings.Repeat("•", len(s))
	}
	return s[:4] + "…" + s[len(s)-4:]
}
