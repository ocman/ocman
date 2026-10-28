package owned

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
)

// ReferenceSchema owns quarantined legacy associations. No hub-allocated token
// is presented as an owner-allocated session ID. Empty scope preserves genuinely
// unattributed legacy records without guessing a platform or machine.
var ReferenceSchema = state.ModuleSchema{
	Module: "platform-session-references",
	Tables: []string{"platform_session_reference", "platform_session_preparation"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE platform_session_reference (
			ref_id TEXT PRIMARY KEY, scope TEXT NOT NULL, native_id TEXT NOT NULL CHECK(native_id<>''),
			canonical_id TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
			UNIQUE(scope,native_id));
			CREATE INDEX platform_session_reference_pending ON platform_session_reference(scope,canonical_id,ref_id);`)
		return err
	}, preparationSchema},
}

type ReferenceStore struct{ db *sql.DB }
type legacyReference struct{ ID, Scope, NativeID, CanonicalID string }

func NewReferences(db *state.DB) (*ReferenceStore, error) {
	if db == nil {
		return nil, sessioncatalog.ErrInvalid
	}
	return &ReferenceStore{db: db.SQL()}, nil
}

func (s *ReferenceStore) reserve(ctx context.Context, scope, nativeID string) (legacyReference, error) {
	if nativeID == "" || identity.IsPendingReference(nativeID) || sessioncatalog.IsID(nativeID) {
		return legacyReference{}, sessioncatalog.ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO platform_session_reference(ref_id,scope,native_id,created_at)
		VALUES(?,?,?,?) ON CONFLICT(scope,native_id) DO NOTHING`, identity.PendingReference(), scope, nativeID, time.Now().UnixMilli())
	if err != nil {
		return legacyReference{}, err
	}
	var row legacyReference
	err = s.db.QueryRowContext(ctx, `SELECT ref_id,scope,native_id,canonical_id FROM platform_session_reference WHERE scope=? AND native_id=?`, scope, nativeID).Scan(&row.ID, &row.Scope, &row.NativeID, &row.CanonicalID)
	return row, err
}

func (s *ReferenceStore) get(ctx context.Context, id string) (legacyReference, error) {
	var row legacyReference
	err := s.db.QueryRowContext(ctx, `SELECT ref_id,scope,native_id,canonical_id FROM platform_session_reference WHERE ref_id=?`, id).Scan(&row.ID, &row.Scope, &row.NativeID, &row.CanonicalID)
	if errors.Is(err, sql.ErrNoRows) {
		return row, sessioncatalog.ErrNotFound
	}
	return row, err
}

// bind is invoked only after the integration has authenticated the owner and
// validated its canonical identity response. A reference cannot be rebound.
func (s *ReferenceStore) bind(ctx context.Context, scope, ref, canonicalID string) error {
	if scope == "" || !identity.IsPendingReference(ref) || !sessioncatalog.IsID(canonicalID) {
		return sessioncatalog.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE platform_session_reference SET canonical_id=?
		WHERE ref_id=? AND scope=? AND (canonical_id='' OR canonical_id=?)`, canonicalID, ref, scope, canonicalID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sessioncatalog.ErrBindingConflict
	}
	return nil
}
