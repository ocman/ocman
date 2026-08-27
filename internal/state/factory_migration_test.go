package state

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigrateV49CreatesIndependentFactorySchema(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ensureSchemaVersionTable(db); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 48; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatalf("apply migration v%d: %v", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at)
		VALUES ('workflow', 'Preserve me', 1, 1, 1);
		INSERT INTO workflow_version
			(id, workflow_id, name, revision, metadata_version, definition_json, concurrency, created_at)
		VALUES ('workflow-v1', 'workflow', 'Preserve me', 1, '1', '{}', 1, 1);
		INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at)
		VALUES ('workflow-run', 'workflow', 'workflow-v1', 'successful', 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var workflowName string
	if err := db.QueryRow(`SELECT name FROM workflow_definition WHERE id = 'workflow'`).Scan(&workflowName); err != nil || workflowName != "Preserve me" {
		t.Fatalf("workflow data after migration = %q, %v", workflowName, err)
	}

	tables := []string{
		"factory_formula", "factory_formula_revision", "factory_attempt", "factory_workspace",
		"factory_delivery", "factory_provider_observation", "factory_profile_validation",
		"factory_authority_exception", "factory_local_execution_ack", "factory_audit_record",
		"factory_external_mapping",
	}
	for _, table := range tables {
		var schema string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&schema); err != nil {
			t.Errorf("missing %s: %v", table, err)
			continue
		}
		if strings.Contains(schema, "workflow_") {
			t.Errorf("%s depends on Workflows schema: %s", table, schema)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO factory_formula (id, name, source, current_revision, created_at, updated_at)
		VALUES ('default', 'Default', 'built_in', 1, 1, 1);
		INSERT INTO factory_formula_revision
			(formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at)
		VALUES ('default', 1, 1, 'formula', 'hash', '{}', 1);
		INSERT INTO factory_attempt
			(id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
		VALUES ('attempt-1', 'epic-1', 'work-1', 1, 'prepared', '{}', 1, 1);
	`); err != nil {
		t.Fatalf("insert valid Factory rows: %v", err)
	}
	for name, statement := range map[string]string{
		"formula source": `INSERT INTO factory_formula (id, name, source, current_revision, created_at, updated_at) VALUES ('bad', 'Bad', 'remote', 1, 1, 1)`,
		"attempt phase":  `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at) VALUES ('bad', 'epic', 'work', 1, 'running', '{}', 1, 1)`,
		"attempt sequence": `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
			VALUES ('duplicate', 'epic-1', 'work-1', 1, 'prepared', '{}', 1, 1)`,
		"session pair": `UPDATE factory_attempt SET session_platform = 'opencode' WHERE id = 'attempt-1'`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Errorf("expected %s constraint failure", name)
		}
	}
}

func TestFactoryLocalExecutionAckIsDurableAndIdempotent(t *testing.T) {
	path := t.TempDir() + "/state.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := time.UnixMilli(1000)
	second := time.UnixMilli(2000)
	for _, at := range []time.Time{first, second} {
		if err := db.UpsertFactoryLocalExecutionAck(context.Background(), "local", "/repo", "factory-plan", "v1", "operator", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acknowledged, err := db.HasFactoryLocalExecutionAck(context.Background(), "local", "/repo", "factory-plan", "v1")
	if err != nil || !acknowledged {
		t.Fatalf("HasFactoryLocalExecutionAck = %v, %v", acknowledged, err)
	}
	acknowledged, err = db.HasFactoryLocalExecutionAck(context.Background(), "local", "/repo", "factory-plan", "v2")
	if err != nil || acknowledged {
		t.Fatalf("HasFactoryLocalExecutionAck for new profile = %v, %v", acknowledged, err)
	}
	var count int
	var actor string
	var acknowledgedAt int64
	if err := db.db.QueryRow(`SELECT count(*), acknowledged_by, acknowledged_at FROM factory_local_execution_ack
		WHERE host_id = 'local' AND repo_root = '/repo' AND profile_id = 'factory-plan' AND profile_version = 'v1'`).Scan(&count, &actor, &acknowledgedAt); err != nil {
		t.Fatal(err)
	}
	if count != 1 || actor != "operator" || acknowledgedAt != second.UnixMilli() {
		t.Fatalf("ack = count %d, actor %q, at %d", count, actor, acknowledgedAt)
	}
}

func TestFactoryMigrationNormalReopenIsIdempotent(t *testing.T) {
	path := t.TempDir() + "/state.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO factory_external_mapping
		(system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		VALUES ('beads', 'instantiation', 'request-1', 'epic', 'epic-1', '{}', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	var epicID string
	if err := db.db.QueryRow(`SELECT entity_id FROM factory_external_mapping WHERE external_id = 'request-1'`).Scan(&epicID); err != nil || epicID != "epic-1" {
		t.Fatalf("mapping after reopen = %q, %v", epicID, err)
	}
	var applied int
	if err := db.db.QueryRow(`SELECT count(*) FROM schema_version WHERE version = 49`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("v49 application count = %d, %v", applied, err)
	}
}
