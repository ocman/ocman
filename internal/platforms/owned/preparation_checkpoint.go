package owned

import (
	"context"
	"database/sql"
)

// preparationSchema keeps cutover receipts separate from user settings. New
// reference proofs invalidate receipts atomically, including proofs saved while
// an owner was briefly connected and applied only on the next safe cutover.
func preparationSchema(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE platform_session_preparation (
		owner_id TEXT NOT NULL, history_path TEXT NOT NULL, signature TEXT NOT NULL,
		PRIMARY KEY(owner_id,history_path));
		CREATE TRIGGER platform_session_reference_preparation_insert
		AFTER INSERT ON platform_session_reference BEGIN
			DELETE FROM platform_session_preparation;
		END;
		CREATE TRIGGER platform_session_reference_preparation_bind
		AFTER UPDATE OF canonical_id ON platform_session_reference
		WHEN old.canonical_id<>new.canonical_id BEGIN
			DELETE FROM platform_session_preparation;
		END;`)
	return err
}

func (s *ReferenceStore) PreparationComplete(ctx context.Context, owner, history, signature string) (bool, error) {
	var complete bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_session_preparation
		WHERE owner_id=? AND history_path=? AND signature=?)`, owner, history, signature).Scan(&complete)
	return complete, err
}

// HasResolvableLocalReferences checks only indexed unresolved references for the
// selected local scopes. Discovery may have supplied a missing binding since
// the last cutover without going through the remote proof store. Do not let a
// completion receipt suppress that recovery, or guess among physical sources.
func (s *ReferenceStore) HasResolvableLocalReferences(ctx context.Context, owner string, scopes []string) (bool, error) {
	for _, scope := range scopes {
		var found bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_session_reference r
			WHERE r.scope=? AND r.canonical_id='' AND
			(SELECT count(*)=1 AND min(c.creation_state)='ready' FROM session_catalog c
			 WHERE c.owner_id=? AND c.platform=r.scope AND c.native_id=r.native_id))`, scope, owner).Scan(&found)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// CompletePreparation is called only after core, history and plugin migrations
// all commit. A crash before this receipt safely repeats the idempotent cutover.
func (s *ReferenceStore) CompletePreparation(ctx context.Context, owner, history, signature string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO platform_session_preparation(owner_id,history_path,signature)
		VALUES(?,?,?) ON CONFLICT(owner_id,history_path) DO UPDATE SET signature=excluded.signature`, owner, history, signature)
	return err
}
