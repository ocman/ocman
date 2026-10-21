package sessioncatalog

import (
	"context"
	"database/sql"
)

func migrateSourceCutover(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_source_cutover(owner_id TEXT NOT NULL,platform TEXT NOT NULL,source_key TEXT NOT NULL,PRIMARY KEY(owner_id,platform,source_key))`)
	return err
}

// RetainSourceLegacyKeys freezes the initial source inventory's key policy in
// the same transaction as its cutover marker. Later startups cannot relabel
// modern identities, even when the source's native inventory changes.
func (s *Store) RetainSourceLegacyKeys(ctx context.Context, source Source) error {
	if source.OwnerID == "" || source.Platform == "" || source.SourceKey == "" {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO session_source_cutover(owner_id,platform,source_key) VALUES(?,?,?) ON CONFLICT DO NOTHING`, source.OwnerID, source.Platform, source.SourceKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE session_catalog SET legacy_keys=1 WHERE owner_id=? AND platform=? AND source_key=?`, source.OwnerID, source.Platform, source.SourceKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}
