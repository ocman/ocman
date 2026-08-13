package state

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// shareTokenBytes is the number of random bytes in a share token.
// 32 bytes = 256 bits of entropy, base64url-encoded to a 43-char
// string. The token is the only secret protecting a shared
// conversation, so it must be long enough to be unguessable.
const shareTokenBytes = 32

// ShareLink is a single public, read-only share of a session's
// conversation. Token is the unguessable secret embedded in the share
// URL. ExpiresAt is reserved for a future expiry feature and is 0 (no
// expiry) for links created by the current UI. RevokedAt is 0 while the
// link is active and set to the revocation time once revoked.
type ShareLink struct {
	Token            string `json:"token"`
	Platform         string `json:"platform"`
	SessionID        string `json:"sessionId"`
	CreatedAt        int64  `json:"createdAt"`
	ExpiresAt        int64  `json:"expiresAt"`
	RevokedAt        int64  `json:"revokedAt"`
	RelayID          string `json:"-"`
	RelayKey         string `json:"-"`
	RelayDeleteToken string `json:"-"`
	RelayURL         string `json:"-"`
	RelayLastSeq     int64  `json:"-"`
}

const shareLinkColumns = `token, platform, session_id, created_at, expires_at, revoked_at,
		relay_id, relay_key, relay_delete_token, relay_url, relay_last_seq`

func scanShareLink(scanner interface{ Scan(...any) error }) (ShareLink, sql.NullInt64, sql.NullInt64, error) {
	var link ShareLink
	var expiresAt, revokedAt sql.NullInt64
	err := scanner.Scan(
		&link.Token, &link.Platform, &link.SessionID, &link.CreatedAt, &expiresAt, &revokedAt,
		&link.RelayID, &link.RelayKey, &link.RelayDeleteToken, &link.RelayURL, &link.RelayLastSeq,
	)
	return link, expiresAt, revokedAt, err
}

// generateShareToken returns a cryptographically random, URL-safe token.
func generateShareToken() (string, error) {
	b := make([]byte, shareTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateShareLink mints a new active share link for the given
// platform/session and returns it. expiresAt is a Unix-ms timestamp, or
// 0 for no expiry.
func (d *DB) CreateShareLink(platform, sessionID string, expiresAt int64) (ShareLink, error) {
	token, err := generateShareToken()
	if err != nil {
		return ShareLink{}, err
	}
	link := ShareLink{
		Token:     token,
		Platform:  platform,
		SessionID: sessionID,
		CreatedAt: time.Now().UnixMilli(),
		ExpiresAt: expiresAt,
	}
	var expiresArg interface{}
	if expiresAt > 0 {
		expiresArg = expiresAt
	}
	_, err = d.db.Exec(`
		INSERT INTO share_link (token, platform, session_id, created_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, NULL)
	`, link.Token, link.Platform, link.SessionID, link.CreatedAt, expiresArg)
	if err != nil {
		return ShareLink{}, fmt.Errorf("creating share link: %w", err)
	}
	return link, nil
}

// GetActiveShareLink returns the share link for a token, but only when
// it is still active (not revoked and not past its expiry). The second
// return value is false when the token is unknown, revoked, or expired —
// callers treat all three identically (404), so they need not be
// distinguished.
func (d *DB) GetActiveShareLink(token string) (ShareLink, bool, error) {
	row := d.db.QueryRow(`
		SELECT `+shareLinkColumns+`
		FROM share_link
		WHERE token = ?
	`, token)
	link, expiresAt, revokedAt, err := scanShareLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ShareLink{}, false, nil
	}
	if err != nil {
		return ShareLink{}, false, fmt.Errorf("getting share link: %w", err)
	}
	if revokedAt.Valid {
		return ShareLink{}, false, nil
	}
	if expiresAt.Valid {
		link.ExpiresAt = expiresAt.Int64
		if expiresAt.Int64 <= time.Now().UnixMilli() {
			return ShareLink{}, false, nil
		}
	}
	return link, true, nil
}

// ListActiveShareLinks returns all non-revoked, non-expired share links
// for a session, newest first.
func (d *DB) ListActiveShareLinks(platform, sessionID string) ([]ShareLink, error) {
	now := time.Now().UnixMilli()
	rows, err := d.db.Query(`
		SELECT `+shareLinkColumns+`
		FROM share_link
		WHERE platform = ? AND session_id = ?
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY created_at DESC
	`, platform, sessionID, now)
	if err != nil {
		return nil, fmt.Errorf("listing share links: %w", err)
	}
	defer rows.Close()

	var out []ShareLink
	for rows.Next() {
		link, expiresAt, _, err := scanShareLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning share link: %w", err)
		}
		if expiresAt.Valid {
			link.ExpiresAt = expiresAt.Int64
		}
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading share links: %w", err)
	}
	return out, nil
}

// ListAllActiveShareLinks returns every non-revoked, non-expired share
// link across all sessions, newest first. Used by the global "shared
// sessions" list in Settings.
func (d *DB) ListAllActiveShareLinks() ([]ShareLink, error) {
	now := time.Now().UnixMilli()
	rows, err := d.db.Query(`
		SELECT `+shareLinkColumns+`
		FROM share_link
		WHERE revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY created_at DESC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("listing share links: %w", err)
	}
	defer rows.Close()

	var out []ShareLink
	for rows.Next() {
		link, expiresAt, _, err := scanShareLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning share link: %w", err)
		}
		if expiresAt.Valid {
			link.ExpiresAt = expiresAt.Int64
		}
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading share links: %w", err)
	}
	return out, nil
}

// SetShareRelay attaches the relay allocation to a local share link.
func (d *DB) SetShareRelay(token, relayURL, relayID, relayKey, deleteToken string) error {
	_, err := d.db.Exec(`UPDATE share_link SET relay_url=?, relay_id=?, relay_key=?, relay_delete_token=?, relay_last_seq=-1 WHERE token=? AND revoked_at IS NULL`, relayURL, relayID, relayKey, deleteToken, token)
	if err != nil {
		return fmt.Errorf("setting share relay: %w", err)
	}
	return nil
}

// SetShareRelaySeq records the highest chunk sequence successfully stored.
func (d *DB) SetShareRelaySeq(token string, seq int64) error {
	_, err := d.db.Exec(`UPDATE share_link SET relay_last_seq=? WHERE token=? AND revoked_at IS NULL`, seq, token)
	if err != nil {
		return fmt.Errorf("setting share relay sequence: %w", err)
	}
	return nil
}

// RevokeShareLink marks a link revoked. Scoped to (platform, sessionID)
// as well as the token so a caller can only revoke a link belonging to
// the session it is acting on. Returns true when a row was revoked,
// false when no matching active link existed (already revoked or
// unknown token) — both are safe outcomes the handler reports as 404.
func (d *DB) RevokeShareLink(platform, sessionID, token string) (bool, error) {
	res, err := d.db.Exec(`
		UPDATE share_link
		SET revoked_at = ?
		WHERE token = ? AND platform = ? AND session_id = ? AND revoked_at IS NULL
	`, time.Now().UnixMilli(), token, platform, sessionID)
	if err != nil {
		return false, fmt.Errorf("revoking share link: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("revoking share link: %w", err)
	}
	return n > 0, nil
}
