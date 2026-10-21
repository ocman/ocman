package statev2

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
)

// SourceInstanceID is the durable source identifier shared by creation metadata
// and ingestion. Keep the original encoding so existing stores never rekey.
func SourceInstanceID(owner, platform, source string) string {
	hash := sha256.Sum256([]byte(owner + "\x00" + platform + "\x00" + source))
	return fmt.Sprintf("source-%x", hash)
}

// Instances lists retained physical sources on one owner/platform, including
// sources that are no longer available for execution.
func (d *DB) Instances(ctx context.Context, owner, platform string) ([]Instance, error) {
	if owner == "" || platform == "" {
		return nil, fmt.Errorf("statev2: owner and platform are required")
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT id,owner_id,platform,source_key,name FROM instance WHERE owner_id=? AND platform=? ORDER BY id`, owner, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		var row Instance
		if err := rows.Scan(&row.ID, &row.OwnerID, &row.Platform, &row.SourceKey, &row.Name); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

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
	err := d.reader().QueryRowContext(ctx, `SELECT id,owner_id,platform,source_key,name FROM instance WHERE id=?`, id).
		Scan(&got.ID, &got.OwnerID, &got.Platform, &got.SourceKey, &got.Name)
	return got, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
