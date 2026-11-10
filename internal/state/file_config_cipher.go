package state

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
)

// Config credentials have their own stable key, independent of cookie-key
// creation/rotation. Both key and ciphertext live in the owner-only database;
// this prevents accidental disclosure, not theft by a database reader.
func (d *DB) configCipher(ctx context.Context, create bool) (cipher.AEAD, error) {
	d.configSecretMu.Lock()
	defer d.configSecretMu.Unlock()
	const keyName = "config.encryption_key"
	var encoded string
	err := d.reader().QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, keyName).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		if !create {
			return nil, errors.New("configuration encryption key is missing")
		}
		var count int
		if err := d.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM setting WHERE key LIKE 'config.secret/%'`).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, errors.New("configuration encryption key is missing; restore it from backup")
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		encoded = base64.RawStdEncoding.EncodeToString(key)
		// Multiple startup instances may race. Only one key wins.
		if _, err := d.db.ExecContext(ctx, `INSERT INTO setting(key,value,updated_at) VALUES(?,?,0) ON CONFLICT(key) DO NOTHING`, keyName, encoded); err != nil {
			return nil, err
		}
		err = d.reader().QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, keyName).Scan(&encoded)
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid configuration encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid configuration encryption key")
	}
	return cipher.NewGCM(block)
}
