package state

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"
)

// Remote is a hub-side record of an attached remote ocman instance.
//
// LocalID is the hub-local surrogate primary key, stable for the life of
// the record even before the remote's real instance ID is learned.
// RemoteID is the learned random instance ID (empty until a successful
// Hello). The token is never exposed on this struct in plaintext; use
// RemoteToken(localID) for the decrypted value when dialing.
type Remote struct {
	LocalID         int64  `json:"localId"`
	RemoteID        string `json:"remoteId,omitempty"`
	DisplayName     string `json:"displayName"`
	Address         string `json:"address"`
	Enabled         bool   `json:"enabled"`
	CreatedAt       int64  `json:"createdAt"`
	LastSeen        int64  `json:"lastSeen"`
	LastHealth      string `json:"lastHealth"`
	Hostname        string `json:"hostname"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// remoteSecretKey is the setting key under which the AES key material is
// stored when no auth_secret is available to derive from (AD-10).
const remoteSecretKey = "remote_token_secret"

// remoteCipher returns an AES-GCM AEAD keyed from the app-local secret.
// It prefers the existing auth_secret HMAC key; if absent it lazily
// generates and persists a dedicated remote_token_secret (AD-10). The
// 32-byte SHA-256 of the source key material is used so AES-256 is used
// regardless of the source key length.
//
// This protects stored remote tokens against casual inspection and
// accidental disclosure. It is NOT designed to withstand an attacker who
// can read both the state DB and the app-local secret (NFR-4, OQ-10).
func (d *DB) remoteCipher(ctx context.Context) (cipher.AEAD, error) {
	keyMaterial, err := d.AuthSecret(ctx)
	if err != nil {
		return nil, err
	}
	if len(keyMaterial) == 0 {
		// No auth secret configured; fall back to a dedicated secret.
		stored, ok, err := d.GetSetting(ctx, remoteSecretKey)
		if err != nil {
			return nil, err
		}
		if ok && stored != "" {
			keyMaterial, err = base64.RawStdEncoding.DecodeString(stored)
			if err != nil {
				return nil, fmt.Errorf("decoding remote secret: %w", err)
			}
		} else {
			keyMaterial = make([]byte, 32)
			if _, err := rand.Read(keyMaterial); err != nil {
				return nil, fmt.Errorf("generating remote secret: %w", err)
			}
			if err := d.SetSetting(ctx, remoteSecretKey, base64.RawStdEncoding.EncodeToString(keyMaterial)); err != nil {
				return nil, err
			}
		}
	}
	sum := sha256.Sum256(keyMaterial)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// encryptToken seals plaintext with AES-GCM. The nonce is prepended to
// the ciphertext so decryptToken is self-contained.
func (d *DB) encryptToken(ctx context.Context, plaintext string) ([]byte, error) {
	aead, err := d.remoteCipher(ctx)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// decryptToken opens a value produced by encryptToken.
func (d *DB) decryptToken(ctx context.Context, sealed []byte) (string, error) {
	aead, err := d.remoteCipher(ctx)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("sealed token too short")
	}
	nonce, ct := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting token: %w", err)
	}
	return string(pt), nil
}

// AddRemote persists a new remote configuration and returns its
// hub-local ID. The token is encrypted at rest. remote_id is left NULL
// until a successful Hello (AD-10b).
func (d *DB) AddRemote(ctx context.Context, address, token, displayName string) (int64, error) {
	enc, err := d.encryptToken(ctx, token)
	if err != nil {
		return 0, err
	}
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO remote (display_name, address, token_encrypted, enabled, created_at)
		 VALUES (?, ?, ?, 1, ?)`,
		displayName, address, enc, time.Now().UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("adding remote: %w", err)
	}
	return res.LastInsertId()
}

const remoteSelectColumns = `local_id, COALESCE(remote_id, ''), display_name, address,
	enabled, created_at, last_seen, last_health, hostname, protocol_version`

func scanRemote(s interface{ Scan(...any) error }) (Remote, error) {
	var r Remote
	var enabled int
	err := s.Scan(&r.LocalID, &r.RemoteID, &r.DisplayName, &r.Address,
		&enabled, &r.CreatedAt, &r.LastSeen, &r.LastHealth, &r.Hostname, &r.ProtocolVersion)
	r.Enabled = enabled != 0
	return r, err
}

// ListRemotes returns every configured remote, ordered by creation time.
func (d *DB) ListRemotes(ctx context.Context) ([]Remote, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+remoteSelectColumns+` FROM remote ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("listing remotes: %w", err)
	}
	defer rows.Close()
	var out []Remote
	for rows.Next() {
		r, err := scanRemote(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning remote: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRemote returns a single remote by hub-local ID.
func (d *DB) GetRemote(ctx context.Context, localID int64) (Remote, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+remoteSelectColumns+` FROM remote WHERE local_id = ?`, localID)
	r, err := scanRemote(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Remote{}, sql.ErrNoRows
	}
	if err != nil {
		return Remote{}, fmt.Errorf("getting remote: %w", err)
	}
	return r, nil
}

// RemoteToken returns the decrypted access token for a remote, used when
// dialing it. Never returned to the browser.
func (d *DB) RemoteToken(ctx context.Context, localID int64) (string, error) {
	var sealed []byte
	err := d.db.QueryRowContext(ctx, `SELECT token_encrypted FROM remote WHERE local_id = ?`, localID).Scan(&sealed)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", fmt.Errorf("reading remote token: %w", err)
	}
	return d.decryptToken(ctx, sealed)
}

// UpdateRemoteConfig edits the operator-controlled fields of a remote.
// A non-nil token replaces the stored token; nil leaves it unchanged.
func (d *DB) UpdateRemoteConfig(ctx context.Context, localID int64, displayName, address string, enabled bool, token *string) error {
	if token != nil {
		enc, err := d.encryptToken(ctx, *token)
		if err != nil {
			return err
		}
		_, err = d.db.ExecContext(ctx,
			`UPDATE remote SET display_name = ?, address = ?, enabled = ?, token_encrypted = ? WHERE local_id = ?`,
			displayName, address, boolToInt(enabled), enc, localID,
		)
		return err
	}
	_, err := d.db.ExecContext(ctx,
		`UPDATE remote SET display_name = ?, address = ?, enabled = ? WHERE local_id = ?`,
		displayName, address, boolToInt(enabled), localID,
	)
	return err
}

// SetRemoteHealth records the latest connection outcome for a remote.
// A learned remoteID (non-empty) is persisted; an empty remoteID leaves
// the stored value untouched so a transient failure doesn't erase it.
func (d *DB) SetRemoteHealth(ctx context.Context, localID int64, remoteID, health, hostname string, protocolVersion int, lastSeen int64) error {
	if remoteID != "" {
		_, err := d.db.ExecContext(ctx,
			`UPDATE remote SET remote_id = ?, last_health = ?, hostname = ?, protocol_version = ?, last_seen = ? WHERE local_id = ?`,
			remoteID, health, hostname, protocolVersion, lastSeen, localID,
		)
		return err
	}
	_, err := d.db.ExecContext(ctx,
		`UPDATE remote SET last_health = ?, last_seen = ? WHERE local_id = ?`,
		health, lastSeen, localID,
	)
	return err
}

// DeleteRemote removes a remote configuration.
func (d *DB) DeleteRemote(ctx context.Context, localID int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM remote WHERE local_id = ?`, localID)
	if err != nil {
		return fmt.Errorf("deleting remote: %w", err)
	}
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
