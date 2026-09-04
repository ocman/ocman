package state

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
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
		"factory_attempt", "factory_workspace",
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
		INSERT INTO factory_attempt
			(id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
		VALUES ('attempt-1', 'epic-1', 'work-1', 1, 'prepared', '{}', 1, 1);
	`); err != nil {
		t.Fatalf("insert valid Factory rows: %v", err)
	}
	for name, statement := range map[string]string{
		"attempt phase": `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at) VALUES ('bad', 'epic', 'work', 1, 'running', '{}', 1, 1)`,
		"attempt sequence": `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
			VALUES ('duplicate', 'epic-1', 'work-1', 1, 'prepared', '{}', 1, 1)`,
		"session pair": `UPDATE factory_attempt SET session_platform = 'opencode' WHERE id = 'attempt-1'`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Errorf("expected %s constraint failure", name)
		}
	}
}

func TestMigrateV66ArchivesRetiredYAMLFormulaLibrary(t *testing.T) {
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
	for version := 1; version <= 65; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatalf("apply migration v%d: %v", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO factory_formula (id, name, source, current_revision, created_at, updated_at)
		VALUES ('legacy', 'Legacy', 'custom', 1, 1, 1);
		INSERT INTO factory_formula_revision
			(formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at)
		VALUES ('legacy', 1, 1, 'name: Legacy', 'legacy-hash', '{}', 1);
		INSERT INTO factory_native_formula (id, name, current_revision, created_at, updated_at)
		VALUES ('native', 'Native', 1, 1, 1);
		INSERT INTO factory_native_formula_revision
			(formula_id, revision, source_toml, compiled_json, content_hash, created_at)
		VALUES ('native', 1, 'name = "Native"', '{}', 'native-hash', 1);
		INSERT INTO factory_external_mapping
			(system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		VALUES ('beads', 'issue', 'bd-1', 'issue', 'bd-1', '{}', 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"factory_formula", "factory_formula_revision"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 0 {
			t.Errorf("retired table %s count = %d, %v", table, count, err)
		}
	}
	var name, definition string
	if err := db.QueryRow(`SELECT name FROM legacy_factory_formula WHERE id = 'legacy'`).Scan(&name); err != nil || name != "Legacy" {
		t.Errorf("archived Formula = %q, %v", name, err)
	}
	if err := db.QueryRow(`SELECT definition_yaml FROM legacy_factory_formula_revision WHERE formula_id = 'legacy' AND revision = 1`).Scan(&definition); err != nil || definition != "name: Legacy" {
		t.Errorf("archived Formula revision = %q, %v", definition, err)
	}
	var source, beadsID string
	if err := db.QueryRow(`SELECT source_toml FROM factory_native_formula_revision WHERE formula_id = 'native'`).Scan(&source); err != nil || source != `name = "Native"` {
		t.Errorf("native Formula after migration = %q, %v", source, err)
	}
	if err := db.QueryRow(`SELECT external_id FROM factory_external_mapping WHERE system = 'beads'`).Scan(&beadsID); err != nil || beadsID != "bd-1" {
		t.Errorf("Beads mapping after migration = %q, %v", beadsID, err)
	}
}

func TestMigrateV67RepairsPreviouslyStampedV66(t *testing.T) {
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
	for version := 1; version <= 65; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatalf("apply migration v%d: %v", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`DROP TABLE factory_formula_revision; DROP TABLE factory_formula; INSERT INTO schema_version (version, applied_at) VALUES (66, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"legacy_factory_formula", "legacy_factory_formula_revision"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Errorf("repair table %s count = %d, %v", table, count, err)
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

func TestFactoryCapacityPolicyIsDurable(t *testing.T) {
	path := t.TempDir() + "/state.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := factory.CapacityPolicy{GlobalCapacity: 12, ProjectCapacity: 3, ProjectOverrides: map[string]int{"/repo": 2}}
	if err := db.SetFactoryCapacityPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.GetFactoryCapacityPolicy(context.Background())
	if err != nil || got.GlobalCapacity != 12 || got.ProjectCapacity != 3 || got.ProjectOverrides["/repo"] != 2 {
		t.Fatalf("capacity policy = %#v, %v", got, err)
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

func TestFactoryGraphMigrationResetsOpaqueGraphIDs(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := ensureSchemaVersionTable(raw); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 62; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatalf("apply migration v%d: %v", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO factory_project (path, created_at) VALUES ('/repo', 0);
		INSERT INTO factory_epic (id, project_path, status, goal, brief, created_at, updated_at) VALUES ('fe_opaque', '/repo', 'open', 'Old graph', '', 0, 0);
		INSERT INTO factory_issue (id, epic_id, kind, title, status, created_at) VALUES ('fe_opaque.plan', 'fe_opaque', 'plan', 'Old plan', 'open', 0);
	`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := migrate(raw); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"factory_project", "factory_epic", "factory_issue"} {
		var count int
		if err := raw.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s after graph reset = %d, %v", table, count, err)
		}
	}
}

func TestFactoryPlanningSessionAndAuditAreDurable(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	session := factory.PlanningSession{Platform: "agent", ID: "session-1"}
	if err := db.PutFactoryPlanningSession(ctx, "epic-1", "work-1", session); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetFactoryPlanningSession(ctx, "work-1")
	if err != nil || !ok || got != session {
		t.Fatalf("planning session = %#v, %v, %v", got, ok, err)
	}
	if err := db.PutFactoryPlanningSession(ctx, "epic-1", "work-1", factory.PlanningSession{Platform: "agent", ID: "session-2"}); err == nil {
		t.Fatal("Planning Session mapping was overwritten")
	}
	if err := db.DeleteFactoryPlanningSession(ctx, "work-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GetFactoryPlanningSession(ctx, "work-1"); err != nil || ok {
		t.Fatalf("deleted Planning Session still present: ok=%v err=%v", ok, err)
	}
	if err := db.PutFactoryPlanningSessionCleanup(ctx, "epic-1", "work-1", session); err != nil {
		t.Fatal(err)
	}
	cleanups, err := db.ListFactoryPlanningSessionCleanups(ctx)
	if err != nil || cleanups["work-1"] != session {
		t.Fatalf("planning cleanup = %#v, %v", cleanups, err)
	}
	if err := db.DeleteFactoryPlanningSessionCleanup(ctx, "work-1"); err != nil {
		t.Fatal(err)
	}
	if cleanups, err := db.ListFactoryPlanningSessionCleanups(ctx); err != nil || len(cleanups) != 0 {
		t.Fatalf("deleted planning cleanup = %#v, %v", cleanups, err)
	}
	if err := db.AppendFactoryAudit(ctx, factory.FactoryAuditRecord{EpicID: "epic-1", WorkID: "work-1", Actor: "dries", Action: "plan.approved", Details: map[string]int{"revision": 2}, At: time.Unix(10, 0)}); err != nil {
		t.Fatal(err)
	}
	var action, details string
	if err := raw.QueryRow(`SELECT action, details_json FROM factory_audit_record WHERE epic_id = 'epic-1'`).Scan(&action, &details); err != nil {
		t.Fatal(err)
	}
	if action != "plan.approved" || details != `{"revision":2}` {
		t.Fatalf("audit = %q, %s", action, details)
	}
	record := factory.FactoryAuditRecord{EpicID: "epic-1", Actor: "dries", Action: "plan.cancelled", Details: map[string]string{"reason": "duplicate"}, At: time.Unix(20, 0)}
	if err := db.AppendFactoryAuditOnce(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendFactoryAuditOnce(ctx, record); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := raw.QueryRow(`SELECT count(*) FROM factory_audit_record WHERE action = 'plan.cancelled'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("deduplicated audit count = %d, err=%v", count, err)
	}
}

func TestFactoryRecoveryGateAuditsEveryResolution(t *testing.T) {
	for _, action := range []string{"resume", "retry", "cancel"} {
		t.Run(action, func(t *testing.T) {
			db, err := Open(t.TempDir() + "/state.db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.db.Exec(`
				INSERT INTO factory_project (path, created_at) VALUES ('/repo', 1);
				INSERT INTO factory_epic (id, project_path, status, goal, brief, created_at, updated_at) VALUES ('epic', '/repo', 'open', 'Goal', '', 1, 1);
				INSERT INTO factory_issue (id, epic_id, kind, title, status, created_at) VALUES ('mol', 'epic', 'mol', 'Mol', 'open', 1), ('work', 'epic', 'implementation', 'Work', 'in_progress', 1);
				INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES ('mol', 'work', 1, 'required');
				INSERT INTO factory_issue_child_sequence (parent_issue_id, next_index) VALUES ('mol', 2);
				INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, session_platform, session_id, frozen_policy_json, created_at, updated_at, started_at) VALUES ('attempt', 'epic', 'work', 1, 'active', 'opencode', 'session', '{"repository":"/repo","profile":"factory-implement/v1"}', 1, 1, 1);
			`); err != nil {
				t.Fatal(err)
			}
			gate, err := db.CreateFactoryRecoveryGate(t.Context(), "attempt", "Choose", "Unsafe to guess", []string{"A", "B"}, time.UnixMilli(10))
			if err != nil {
				t.Fatal(err)
			}
			resolved, attempt, err := db.ResolveFactoryRecoveryGate(t.Context(), gate.IssueID, action, "A", time.UnixMilli(20))
			if err != nil || resolved.Resolution != action || attempt.ID != "attempt" {
				t.Fatalf("ResolveFactoryRecoveryGate = %#v, %#v, %v", resolved, attempt, err)
			}
			var requested, resolvedCount int
			if err := db.db.QueryRow(`SELECT count(*) FROM factory_audit_record WHERE action = 'recovery.requested'`).Scan(&requested); err != nil {
				t.Fatal(err)
			}
			if err := db.db.QueryRow(`SELECT count(*) FROM factory_audit_record WHERE action = ?`, "recovery."+action).Scan(&resolvedCount); err != nil {
				t.Fatal(err)
			}
			if requested != 1 || resolvedCount != 1 {
				t.Fatalf("audit request/resolve = %d/%d", requested, resolvedCount)
			}
		})
	}
}
