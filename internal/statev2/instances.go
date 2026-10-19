package statev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RegisterInstance creates an immutable source identity or updates its display
// name. Neither an ID nor an owner/platform/source tuple can be rebound.
func (d *DB) RegisterInstance(ctx context.Context, instance Instance) error {
	if instance.ID == "" || instance.OwnerID == "" || instance.Platform == "" || instance.SourceKey == "" {
		return fmt.Errorf("statev2: instance identity fields must not be empty")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance(id,owner_id,platform,source_key,name)
		VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, instance.ID, instance.OwnerID,
		instance.Platform, instance.SourceKey, instance.Name); err != nil {
		return err
	}
	var got Instance
	err = tx.QueryRowContext(ctx, `SELECT id,owner_id,platform,source_key,name FROM instance WHERE id=?`, instance.ID).
		Scan(&got.ID, &got.OwnerID, &got.Platform, &got.SourceKey, &got.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIdentityConflict
	}
	if err != nil {
		return err
	}
	if got.OwnerID != instance.OwnerID || got.Platform != instance.Platform || got.SourceKey != instance.SourceKey {
		return ErrIdentityConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE instance SET name=? WHERE id=?`, instance.Name, instance.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) Instance(ctx context.Context, id string) (Instance, error) {
	var got Instance
	err := d.db.QueryRowContext(ctx, `SELECT id,owner_id,platform,source_key,name FROM instance WHERE id=?`, id).
		Scan(&got.ID, &got.OwnerID, &got.Platform, &got.SourceKey, &got.Name)
	return got, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
