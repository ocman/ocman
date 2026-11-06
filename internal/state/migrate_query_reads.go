package state

import "database/sql"

// v132 indexes scoped history reads and recurring maintenance predicates. Invalid
// legacy payloads get a separate small index, so upgrades remain possible while
// history reads can still fail closed without scanning every notification.
func migrateQueryReadIndexes(tx *sql.Tx) error {
	for _, index := range []struct{ table, ddl string }{
		{"inbox_item", `CREATE INDEX IF NOT EXISTS inbox_permission_identity ON inbox_item (
			CAST(json_extract(permission_json,'$.platform') AS TEXT),
			CAST(json_extract(permission_json,'$.sessionId') AS TEXT),
			CAST(json_extract(permission_json,'$.permissionId') AS TEXT)
		) WHERE category='permission' AND permission_json!='' AND json_valid(permission_json);
		CREATE INDEX IF NOT EXISTS inbox_permission_invalid ON inbox_item(id)
			WHERE category='permission' AND permission_json!='' AND NOT json_valid(permission_json);
		CREATE INDEX IF NOT EXISTS inbox_permission_pending ON inbox_item(created_at DESC,id DESC)
			WHERE archived_at IS NULL AND category='permission' AND permission_json!=''`},
		{"permission_lifecycle", `CREATE INDEX IF NOT EXISTS permission_lifecycle_requested ON permission_lifecycle(requested_at)`},
		{"database_size_sample", `CREATE INDEX IF NOT EXISTS database_size_sample_window ON database_size_sample(sampled_at,database)`},
		{"webhook_delivery", `CREATE INDEX IF NOT EXISTS webhook_delivery_retention ON webhook_delivery(accepted_at) WHERE accepted_at>0`},
		{"webhook_dispatch", `CREATE INDEX IF NOT EXISTS webhook_dispatch_queued_routine ON webhook_dispatch(routine_id) WHERE state='queued'`},
		{"factory_project_request_gate", `CREATE INDEX IF NOT EXISTS factory_project_request_plan ON factory_project_request_gate(plan_issue_id) WHERE resolution='approved'`},
	} {
		// Older repaired/stamped databases may not contain unrelated feature
		// tables. Follow the core migration convention of preserving that state.
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, index.table).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err := tx.Exec(index.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}
