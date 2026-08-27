package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HasFactoryLocalExecutionAck reports whether the operator has acknowledged
// one host, repository, and permission profile tuple.
func (d *DB) HasFactoryLocalExecutionAck(ctx context.Context, hostID, repoRoot, profileID, profileVersion string) (bool, error) {
	var acknowledged int
	err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_local_execution_ack
		WHERE host_id = ? AND repo_root = ? AND profile_id = ? AND profile_version = ?`,
		hostID, repoRoot, profileID, profileVersion).Scan(&acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking Factory local execution acknowledgement: %w", err)
	}
	return true, nil
}

// UpsertFactoryLocalExecutionAck records the operator's acknowledgement for
// one host, repository, and permission profile tuple.
func (d *DB) UpsertFactoryLocalExecutionAck(ctx context.Context, hostID, repoRoot, profileID, profileVersion, actor string, at time.Time) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO factory_local_execution_ack
			(host_id, repo_root, profile_id, profile_version, acknowledged_by, acknowledged_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id, repo_root, profile_id, profile_version) DO UPDATE SET
			acknowledged_by = excluded.acknowledged_by,
			acknowledged_at = excluded.acknowledged_at
	`, hostID, repoRoot, profileID, profileVersion, actor, at.UnixMilli())
	if err != nil {
		return fmt.Errorf("upserting Factory local execution acknowledgement: %w", err)
	}
	return nil
}
