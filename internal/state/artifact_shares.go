package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ArtifactShare is one relay publication of an artifact. The relay key and
// delete token are secrets: they only ever leave through localhost routes.
type ArtifactShare struct {
	ID               string
	ArtifactID       string
	RelayID          string
	RelayKey         string
	RelayDeleteToken string
	RelayURL         string
	CreatedAt        int64
	RevokedAt        int64 // 0 while active
}

const artifactShareColumns = `id,artifact_id,relay_id,relay_key,relay_delete_token,relay_url,created_at,COALESCE(revoked_at,0)`

func scanArtifactShare(row interface{ Scan(...any) error }) (ArtifactShare, error) {
	var s ArtifactShare
	err := row.Scan(&s.ID, &s.ArtifactID, &s.RelayID, &s.RelayKey, &s.RelayDeleteToken, &s.RelayURL, &s.CreatedAt, &s.RevokedAt)
	return s, err
}

// CreateArtifactShare records a completed relay publication.
func (d *DB) CreateArtifactShare(ctx context.Context, s ArtifactShare) (ArtifactShare, error) {
	s.ID = strings.ToLower(rand.Text())
	s.CreatedAt = time.Now().UnixMilli()
	s.RevokedAt = 0
	res, err := d.db.ExecContext(ctx, `INSERT INTO artifact_share (id,artifact_id,relay_id,relay_key,relay_delete_token,relay_url,created_at)
		SELECT ?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM artifact WHERE id=?)`,
		s.ID, s.ArtifactID, s.RelayID, s.RelayKey, s.RelayDeleteToken, s.RelayURL, s.CreatedAt, s.ArtifactID)
	if err != nil {
		return ArtifactShare{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ArtifactShare{}, ErrArtifactNotFound
	}
	return s, nil
}

// ListArtifactShares returns an artifact's shares, newest first, revoked included.
func (d *DB) ListArtifactShares(ctx context.Context, artifactID string) ([]ArtifactShare, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+artifactShareColumns+` FROM artifact_share WHERE artifact_id=? ORDER BY created_at DESC, id`, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArtifactShare{}
	for rows.Next() {
		s, err := scanArtifactShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetArtifactShare returns one share of an artifact.
func (d *DB) GetArtifactShare(ctx context.Context, artifactID, id string) (ArtifactShare, error) {
	s, err := scanArtifactShare(d.db.QueryRowContext(ctx, `SELECT `+artifactShareColumns+` FROM artifact_share WHERE artifact_id=? AND id=?`, artifactID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactShare{}, ErrArtifactNotFound
	}
	return s, err
}

// RevokeArtifactShare stamps revoked_at; revoking twice keeps the first stamp.
func (d *DB) RevokeArtifactShare(ctx context.Context, artifactID, id string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE artifact_share SET revoked_at=? WHERE artifact_id=? AND id=? AND revoked_at IS NULL`,
		time.Now().UnixMilli(), artifactID, id)
	return err
}
