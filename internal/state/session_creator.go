package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionCreator records application-level launch provenance. It is independent
// of native parent IDs and never implies permission or lifecycle inheritance.
// Each platform includes its machine routing scope; projects may differ.
type SessionCreator struct {
	Platform         string `json:"platform"`
	SessionID        string `json:"sessionId"`
	CreatorPlatform  string `json:"creatorPlatform"`
	CreatorSessionID string `json:"creatorSessionId"`
	CreatedAt        int64  `json:"createdAt"`
}

// v131 keeps creator associations in application state, not native history.
func migrateSessionCreator(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_creator (
		platform TEXT NOT NULL CHECK (platform <> ''),
		session_id TEXT NOT NULL CHECK (session_id <> ''),
		creator_platform TEXT NOT NULL CHECK (creator_platform <> ''),
		creator_session_id TEXT NOT NULL CHECK (creator_session_id <> ''),
		created_at INTEGER NOT NULL,
		PRIMARY KEY (platform, session_id),
		CHECK (platform <> creator_platform OR session_id <> creator_session_id)
	);
	CREATE INDEX IF NOT EXISTS session_creator_parent ON session_creator (creator_platform, creator_session_id)`)
	return err
}

// RecordSessionCreator is idempotent for the same association and refuses to
// overwrite an existing creator. Call before delivering the first prompt.
func (d *DB) RecordSessionCreator(ctx context.Context, creator SessionCreator) error {
	if creator.CreatedAt == 0 {
		creator.CreatedAt = time.Now().UnixMilli()
	}
	var id string
	err := d.db.QueryRowContext(ctx, `INSERT INTO session_creator
		(platform, session_id, creator_platform, creator_session_id, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (platform, session_id) DO UPDATE SET session_id=excluded.session_id
		WHERE creator_platform=excluded.creator_platform AND creator_session_id=excluded.creator_session_id
		RETURNING session_id`, creator.Platform, creator.SessionID, creator.CreatorPlatform, creator.CreatorSessionID, creator.CreatedAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session already has a different creator")
	}
	return err
}

// GetSessionCreator returns nil for sessions created without caller identity.
func (d *DB) GetSessionCreator(ctx context.Context, platform, sessionID string) (*SessionCreator, error) {
	creator := &SessionCreator{Platform: platform, SessionID: sessionID}
	err := d.db.QueryRowContext(ctx, `SELECT creator_platform, creator_session_id, created_at
		FROM session_creator WHERE platform=? AND session_id=?`, platform, sessionID).
		Scan(&creator.CreatorPlatform, &creator.CreatorSessionID, &creator.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return creator, nil
}
