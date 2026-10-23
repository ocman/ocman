package statev2

import (
	"context"
	"database/sql"
	"errors"
)

const preparationSchema = `CREATE TABLE session_preparation (
	owner_id TEXT PRIMARY KEY, receipt TEXT NOT NULL);
	CREATE TABLE session_identity_plan (
		instance_id TEXT NOT NULL REFERENCES instance(id), native_id TEXT NOT NULL,
		canonical_id TEXT NOT NULL, PRIMARY KEY(instance_id,native_id),
		UNIQUE(instance_id,canonical_id));
	CREATE TABLE session_identity_inventory (owner_id TEXT PRIMARY KEY);
	CREATE TRIGGER session_preparation_legacy_insert AFTER INSERT ON session
	WHEN substr(new.id,1,4)<>'ocm_' BEGIN
		DELETE FROM session_preparation WHERE owner_id=(SELECT owner_id FROM instance WHERE id=new.instance_id);
	END;`

// PreparationReceipt binds the core cutover checkpoint to this history database,
// not just its pathname. Replacing or restoring either database must not let a
// receipt from the other database bypass identity adoption.
func (d *DB) PreparationReceipt(ctx context.Context, owner string) (string, error) {
	var receipt string
	err := d.reader().QueryRowContext(ctx, `SELECT receipt FROM session_preparation WHERE owner_id=?`, owner).Scan(&receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return receipt, err
}

func (d *DB) CompletePreparation(ctx context.Context, owner, receipt string) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO session_preparation(owner_id,receipt) VALUES(?,?)
		ON CONFLICT(owner_id) DO UPDATE SET receipt=excluded.receipt`, owner, receipt)
	return err
}
