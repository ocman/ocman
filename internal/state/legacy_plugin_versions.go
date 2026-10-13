package state

import (
	"database/sql"
	"fmt"
)

// rewindLegacyPluginVersions repairs development databases from the plugin
// branch, which recorded its tables as core v122-v124 before main took v122
// and v123. Their stamped version would skip main's repair and snooze
// migrations (or exceed this build's latest version), so the stamps above
// v121 are dropped and replayed; main's v122/v123 are idempotent. The tables
// stay, and the plugins ModuleSchema adopts them with IF NOT EXISTS.
// Detection is the plugin table without its module ledger row, which only
// such a database has.
func rewindLegacyPluginVersions(db *sql.DB) error {
	var legacy bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='plugin_permission_profiles')`).Scan(&legacy)
	if err == nil && legacy {
		// No module ledger at all, or one without the plugins module.
		err = db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='module_schema_version')`).Scan(&legacy)
		if err == nil && !legacy {
			err = db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM module_schema_version WHERE module='plugins')`).Scan(&legacy)
		}
	}
	if err != nil || !legacy {
		return err
	}
	if _, err := db.Exec(`DELETE FROM schema_version WHERE version > 121`); err != nil {
		return fmt.Errorf("rewinding legacy plugin schema versions: %w", err)
	}
	return nil
}
