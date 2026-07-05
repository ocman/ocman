package state

import (
	"database/sql"
	"fmt"
	"time"
)

// AuthSecret returns the persisted HMAC key used to sign auth
// cookies, or nil if none has been stored yet. The same key is
// reused across restarts so logged-in clients stay logged in up
// to the cookie TTL.
func (d *DB) AuthSecret() ([]byte, error) {
	var key []byte
	err := d.db.QueryRow(`SELECT hmac_key FROM auth_secret WHERE id = 1`).Scan(&key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading auth_secret: %w", err)
	}
	return key, nil
}

// SetAuthSecret overwrites the persisted HMAC key. Existing cookies
// signed with the previous key become invalid, which is the intended
// behaviour of a rotation.
func (d *DB) SetAuthSecret(key []byte) error {
	_, err := d.db.Exec(`
		INSERT INTO auth_secret (id, hmac_key, created_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			hmac_key = excluded.hmac_key,
			created_at = excluded.created_at
	`, key, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("writing auth_secret: %w", err)
	}
	return nil
}
