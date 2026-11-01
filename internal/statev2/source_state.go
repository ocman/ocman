package statev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SourceState returns an instance's ingestion bookkeeping, such as source
// version hints and verification times. It is advisory: losing it only causes
// extra source reads, never lost or duplicated history.
func (d *DB) SourceState(ctx context.Context, instanceID string) (map[string]string, error) {
	rows, err := d.reader().QueryContext(ctx, `SELECT key,value FROM source_state WHERE instance_id=?`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// SourceValue reads one bookkeeping value; a missing key is empty.
func (d *DB) SourceValue(ctx context.Context, instanceID, key string) (string, error) {
	var value string
	err := d.reader().QueryRowContext(ctx, `SELECT value FROM source_state WHERE instance_id=? AND key=?`, instanceID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetSourceState stores one bookkeeping value; an empty value removes it.
func (d *DB) SetSourceState(ctx context.Context, instanceID, key, value string) error {
	return setSourceState(ctx, d.db, instanceID, key, value)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func setSourceState(ctx context.Context, q execer, instanceID, key, value string) error {
	if instanceID == "" || key == "" {
		return fmt.Errorf("statev2: source state requires an instance and key")
	}
	if value == "" {
		_, err := q.ExecContext(ctx, `DELETE FROM source_state WHERE instance_id=? AND key=?`, instanceID, key)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO source_state(instance_id,key,value) VALUES(?,?,?)
		ON CONFLICT(instance_id,key) DO UPDATE SET value=excluded.value`, instanceID, key, value)
	return err
}

// SourceUpdate is ingestion bookkeeping committed in the same transaction as
// a session's content, so a read receipt never describes content that is not
// yet durable and one import costs one write transaction.
type SourceUpdate struct {
	// State applies unconditionally; an empty value removes the key.
	State map[string]string
	// Verify, when set, must match the session's retained usage after the
	// content writes. On a mismatch Verified and Streams are skipped and the
	// result reports it. Nil trusts the content.
	Verify *Usage
	// Verified applies after State once verification passes.
	Verified map[string]string
	// Streams sets checkpoint cursors once verification passes.
	Streams map[string]string
}

// ApplySource commits bookkeeping for content that did not change, in one
// transaction. It reports whether the retained usage failed verification.
func (d *DB) ApplySource(ctx context.Context, key Key, u SourceUpdate) (mismatch bool, err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if mismatch, err = applySource(ctx, tx, key, &u); err != nil {
		return false, err
	}
	return mismatch, tx.Commit()
}

func applySource(ctx context.Context, tx *sql.Tx, key Key, u *SourceUpdate) (bool, error) {
	for k, v := range u.State {
		if err := setSourceState(ctx, tx, key.InstanceID, k, v); err != nil {
			return false, err
		}
	}
	if u.Verify != nil {
		got, err := usage(ctx, tx, key)
		if err != nil {
			return false, err
		}
		if !u.Verify.Matches(got) {
			return true, nil
		}
	}
	for k, v := range u.Verified {
		if err := setSourceState(ctx, tx, key.InstanceID, k, v); err != nil {
			return false, err
		}
	}
	for stream, cursor := range u.Streams {
		if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoint(instance_id,stream,cursor) VALUES(?,?,?)
			ON CONFLICT(instance_id,stream) DO UPDATE SET cursor=excluded.cursor`, key.InstanceID, stream, cursor); err != nil {
			return false, err
		}
	}
	return false, nil
}
