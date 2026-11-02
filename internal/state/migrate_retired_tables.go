package state

import "database/sql"

// retiredTables have no reader outside the migrations that created or filled
// them: the workflow engine and agent loops it replaced, prompt schedules, the
// archived Formula copies and Factory's never-written audit tables. Children
// come before their parents. factory_workspace stays: factory_delivery still
// declares a foreign key to it.
var retiredTables = []string{
	"workflow_map_item", "workflow_resource_lease", "workflow_workspace_lease",
	"workflow_artifact", "workflow_trigger_firing", "workflow_trigger_state",
	"workflow_node_attempt", "workflow_node_run", "workflow_run",
	"workflow_version_dependency", "workflow_version_node", "workflow_version",
	"workflow_definition", "loop_workflow_map", "loop_iterations", "loops",
	"prompt_schedule", "legacy_factory_formula_revision", "legacy_factory_formula",
	"factory_provider_observation", "factory_profile_validation",
	"factory_authority_exception",
}

func migrateDropRetiredTables(tx *sql.Tx) error {
	// workflow_definition and workflow_version reference each other.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	for _, table := range retiredTables {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			return err
		}
	}
	return nil
}
