package statev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PlanSessionRekeys durably stages identity mappings without modifying metadata
// pagination keys. This bounds memory while retaining the inventory-before-rekey
// ordering required by lexical session cursors. Replays must agree on identity.
func (d *DB) PlanSessionRekeys(ctx context.Context, instance string, identities map[string]string) error {
	if len(identities) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for native, canonical := range identities {
		if native == "" || canonical == "" || native == canonical {
			return fmt.Errorf("statev2: invalid session identity plan: %w", ErrIdentityConflict)
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO session_identity_plan(instance_id,native_id,canonical_id)
			VALUES(?,?,?) ON CONFLICT(instance_id,native_id) DO UPDATE SET canonical_id=excluded.canonical_id
			WHERE session_identity_plan.canonical_id=excluded.canonical_id`, instance, native, canonical)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrIdentityConflict
		}
	}
	return tx.Commit()
}

// ApplyPlannedSessionRekeys runs only with ingestion/writers stopped. Every batch
// is atomic, and deleting its plan after commit is replayable if interrupted.
// One backup per invocation covers all subsequent batches and physical sources.
func (d *DB) ApplyPlannedSessionRekeys(ctx context.Context, owner string, report func(int)) error {
	backedUp, completed := false, 0
	for {
		instance, mapping, err := d.sessionRekeyPage(ctx, owner)
		if err != nil || len(mapping) == 0 {
			return err
		}
		if !backedUp {
			if err := d.backupSessionKeys(ctx); err != nil {
				return err
			}
			backedUp = true
		}
		if err := d.rekeySessions(ctx, instance, mapping); err != nil {
			return err
		}
		if err := d.forgetSessionRekeyPage(ctx, instance, mapping); err != nil {
			return err
		}
		completed += len(mapping)
		if report != nil {
			report(completed)
		}
	}
}

func (d *DB) sessionRekeyPage(ctx context.Context, owner string) (string, map[string]string, error) {
	var instance string
	err := d.db.QueryRowContext(ctx, `SELECT p.instance_id FROM session_identity_plan p
		JOIN instance i ON i.id=p.instance_id WHERE i.owner_id=? ORDER BY p.instance_id LIMIT 1`, owner).Scan(&instance)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT native_id,canonical_id FROM session_identity_plan
		WHERE instance_id=? ORDER BY native_id LIMIT 500`, instance)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	mapping := map[string]string{}
	for rows.Next() {
		var native, canonical string
		if err := rows.Scan(&native, &canonical); err != nil {
			return "", nil, err
		}
		mapping[native] = canonical
	}
	return instance, mapping, rows.Err()
}

// A completed inventory must survive interruptions during batch application.
// Otherwise restarting discovery could mistake an already-rekeyed parent ID in
// a not-yet-rekeyed child for a native parent and allocate a second identity.
func (d *DB) SessionRekeyInventoryReady(ctx context.Context, owner string) (bool, error) {
	var ready bool
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_identity_inventory WHERE owner_id=?)`, owner).Scan(&ready)
	return ready, err
}

func (d *DB) SetSessionRekeyInventoryReady(ctx context.Context, owner string, ready bool) error {
	if ready {
		_, err := d.db.ExecContext(ctx, `INSERT INTO session_identity_inventory(owner_id) VALUES(?) ON CONFLICT DO NOTHING`, owner)
		return err
	}
	_, err := d.db.ExecContext(ctx, `DELETE FROM session_identity_inventory WHERE owner_id=?`, owner)
	return err
}

func (d *DB) forgetSessionRekeyPage(ctx context.Context, instance string, mapping map[string]string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for native, canonical := range mapping {
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_identity_plan WHERE instance_id=? AND native_id=? AND canonical_id=?`, instance, native, canonical); err != nil {
			return err
		}
	}
	return tx.Commit()
}
