package sessioncatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Reserve persists complete creation/filter metadata before a caller performs
// platform side effects. Created=false is a replay, never permission to create
// another native execution (including after a failed or interrupted request).
func (s *Store) Reserve(ctx context.Context, req Reservation) (record Session, created bool, err error) {
	if !validReservation(req) {
		return record, false, ErrInvalid
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return record, false, err
	}
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return record, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var id, previous string
	err = tx.QueryRowContext(ctx, `SELECT id,request_fingerprint FROM session_catalog WHERE owner_id=? AND request_key=?`, req.OwnerID, req.RequestKey).Scan(&id, &previous)
	if err == nil {
		if previous != fingerprint {
			return record, false, ErrRequestConflict
		}
		record, err = get(ctx, tx, id)
		return record, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return record, false, err
	}
	if err := validParent(ctx, tx, req.OwnerID, req.ParentID); err != nil {
		return record, false, err
	}
	now := time.Now().UnixMilli()
	record = Session{ID: "ocm_" + uuid.NewString(), OwnerID: req.OwnerID, Platform: req.Platform,
		Directory: req.Directory, Title: req.Title, ParentID: req.ParentID, Origin: req.Origin, OriginID: req.OriginID,
		CreationState: Creating, CreatedAt: now, UpdatedAt: now}
	if err := insert(ctx, tx, record, req.RequestKey, fingerprint); err != nil {
		return Session{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, false, err
	}
	return record, true, nil
}

func validReservation(r Reservation) bool {
	if !nonempty(r.RequestKey) || !nonempty(r.OwnerID) || !nonempty(r.Platform) || !nonempty(r.Directory) {
		return false
	}
	digest, err := hex.DecodeString(r.PayloadHash)
	if err != nil || len(digest) != sha256.Size {
		return false
	}
	switch r.Origin {
	case User:
		return r.OriginID == ""
	case Routine, Factory:
		return nonempty(r.OriginID)
	default:
		return false
	}
}

func insert(ctx context.Context, tx *sql.Tx, s Session, request, fingerprint string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO session_catalog
		(id,owner_id,platform,directory,title,parent_id,origin,origin_id,creation_state,creation_error,
		 request_key,request_fingerprint,source_key,native_id,created_at,updated_at,legacy_keys)
		VALUES (?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.OwnerID, s.Platform, s.Directory, s.Title, s.ParentID, s.Origin, s.OriginID, s.CreationState, s.CreationError,
		request, fingerprint, s.SourceKey, s.NativeID, s.CreatedAt, s.UpdatedAt, s.LegacyKeys)
	return err
}
