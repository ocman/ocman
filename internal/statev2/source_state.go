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
	rows, err := d.db.QueryContext(ctx, `SELECT key,value FROM source_state WHERE instance_id=?`, instanceID)
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
	err := d.db.QueryRowContext(ctx, `SELECT value FROM source_state WHERE instance_id=? AND key=?`, instanceID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetSourceState stores one bookkeeping value; an empty value removes it.
func (d *DB) SetSourceState(ctx context.Context, instanceID, key, value string) error {
	if instanceID == "" || key == "" {
		return fmt.Errorf("statev2: source state requires an instance and key")
	}
	if value == "" {
		_, err := d.db.ExecContext(ctx, `DELETE FROM source_state WHERE instance_id=? AND key=?`, instanceID, key)
		return err
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO source_state(instance_id,key,value) VALUES(?,?,?)
		ON CONFLICT(instance_id,key) DO UPDATE SET value=excluded.value`, instanceID, key, value)
	return err
}
