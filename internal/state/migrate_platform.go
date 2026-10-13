package state

import "database/sql"

func migrateToV112(tx *sql.Tx) error {
	for _, table := range []string{"routine", "factory_epic"} {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, table).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if err := addColumnIfMissing(tx, table, "platform", "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
			// Before v112 every saved create target was OpenCode-only.
			if _, err := tx.Exec(`UPDATE ` + table + ` SET platform = 'opencode' WHERE platform = ''`); err != nil {
				return err
			}
		}
	}
	var attempts bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'factory_attempt')`).Scan(&attempts); err != nil {
		return err
	}
	if attempts {
		if _, err := tx.Exec(`UPDATE factory_attempt SET frozen_policy_json = json_set(frozen_policy_json, '$.platform', 'opencode') WHERE COALESCE(json_extract(frozen_policy_json, '$.platform'), '') = ''`); err != nil {
			return err
		}
	}
	return nil
}
