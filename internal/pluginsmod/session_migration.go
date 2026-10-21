package pluginsmod

import (
	"context"
	"database/sql"

	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/state"
)

type sessionAssociation struct{ platform, session, plugin string }
type runAssociation struct{ plugin, operation, session string }

// MigrateSessionReferences rekeys session.v1 mappings, owed completions and run
// receipts before any plugin processes or state users start. The platform-owned
// resolver supplies canonical IDs or quarantine tokens, never a guessed owner.
// Fingerprints, settled/error flags, operation IDs and outbox sequences remain
// unchanged. Collisions roll back rather than dropping work or merging claims.
func (d *Store) MigrateSessionReferences(ctx context.Context, core *state.DB, resolve func(context.Context, string, string) (string, error)) error {
	if core == nil || core.SQL() != d.db || resolve == nil {
		return state.ErrPluginInvalid
	}
	associations, runs, err := d.sessionMigrationInventory(ctx)
	if err != nil {
		return err
	}
	type key struct{ platform, session string }
	mapping := map[key]string{}
	project := func(platform, session string) (string, error) {
		k := key{platform, session}
		if id, ok := mapping[k]; ok {
			return id, nil
		}
		id, err := resolve(ctx, platform, session)
		if err != nil {
			return "", err
		}
		if !identity.IsSessionID(id) && !identity.IsPendingReference(id) {
			return "", state.ErrPluginInvalid
		}
		mapping[k] = id
		return id, nil
	}
	// Run receipts predate a platform column. Infer it only from the same
	// plugin's durable mapping/outbox, never from an installation-wide default.
	scopes := map[[2]string]map[string]bool{}
	for _, ref := range associations {
		k := [2]string{ref.plugin, ref.session}
		if scopes[k] == nil {
			scopes[k] = map[string]bool{}
		}
		scopes[k][ref.platform] = true
		if _, err := project(ref.platform, ref.session); err != nil {
			return err
		}
	}
	runTargets := make([]string, len(runs))
	changed := false
	for k, id := range mapping {
		changed = changed || k.session != id
	}
	for i, run := range runs {
		var platform string
		if owners := scopes[[2]string{run.plugin, run.session}]; len(owners) == 1 {
			for scope := range owners {
				platform = scope
			}
		}
		id, err := project(platform, run.session)
		if err != nil {
			return err
		}
		runTargets[i] = id
		changed = changed || run.session != id
	}
	if !changed {
		return nil
	}
	if err := core.BackupSessionReferences(); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, id := range mapping {
		if k.session == id {
			continue
		}
		for _, table := range []string{"plugin_session", "plugin_session_outbox"} {
			if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET session_id=? WHERE platform_id=? AND session_id=?", id, k.platform, k.session); err != nil {
				return err
			}
		}
	}
	for i, run := range runs {
		if run.session == runTargets[i] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_session_run SET session_id=? WHERE plugin_id=? AND operation_id=? AND session_id=?`, runTargets[i], run.plugin, run.operation, run.session); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Store) sessionMigrationInventory(ctx context.Context) ([]sessionAssociation, []runAssociation, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT platform_id,session_id,plugin_id FROM plugin_session WHERE session_id<>'' UNION SELECT platform_id,session_id,plugin_id FROM plugin_session_outbox WHERE session_id<>''`)
	if err != nil {
		return nil, nil, err
	}
	associations, err := scanSessionAssociations(rows)
	if err != nil {
		return nil, nil, err
	}
	rows, err = d.db.QueryContext(ctx, `SELECT plugin_id,operation_id,session_id FROM plugin_session_run WHERE session_id<>''`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var runs []runAssociation
	for rows.Next() {
		var run runAssociation
		if err := rows.Scan(&run.plugin, &run.operation, &run.session); err != nil {
			return nil, nil, err
		}
		runs = append(runs, run)
	}
	return associations, runs, rows.Err()
}

func scanSessionAssociations(rows *sql.Rows) ([]sessionAssociation, error) {
	defer rows.Close()
	var associations []sessionAssociation
	for rows.Next() {
		var ref sessionAssociation
		if err := rows.Scan(&ref.platform, &ref.session, &ref.plugin); err != nil {
			return nil, err
		}
		associations = append(associations, ref)
	}
	return associations, rows.Err()
}
