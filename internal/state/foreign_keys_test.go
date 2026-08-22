package state

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// fkViolation is one row from PRAGMA foreign_key_check.
type fkViolation struct {
	Table  string
	RowID  sql.NullInt64
	Parent string
	FKID   int
}

// checkForeignKeys runs PRAGMA foreign_key_check over the whole database
// and returns every declared-constraint violation it finds.
func checkForeignKeys(t *testing.T, db *sql.DB) []fkViolation {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	var out []fkViolation
	for rows.Next() {
		var v fkViolation
		if err := rows.Scan(&v.Table, &v.RowID, &v.Parent, &v.FKID); err != nil {
			t.Fatalf("scanning foreign_key_check row: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading foreign_key_check: %v", err)
	}
	return out
}

// TestForeignKeysEnabledOnCleanDatabase pins that a database satisfying
// its declared constraints gets enforcement turned on. SQLite defaults
// foreign_keys OFF and nothing set it, so every REFERENCES clause in the
// workflow tables was decorative.
func TestForeignKeysEnabledOnCleanDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	var on int
	if err := db.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatalf("reading foreign_keys pragma: %v", err)
	}
	if on != 1 {
		t.Errorf("foreign_keys = %d, want 1 on a clean database", on)
	}
}

// TestForeignKeysStayOffOnDirtyDatabase is the guard: an existing
// install whose rows already violate a declared constraint must keep
// starting normally, with enforcement left off, rather than erroring on
// every subsequent write.
func TestForeignKeysStayOffOnDirtyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	seed, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Enforcement is on after a clean open, so turn it off to plant a
	// row that violates workflow_run -> workflow_version.
	if _, err := seed.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.Exec(`INSERT INTO workflow_run
		(id, workflow_id, version_id, state, created_at, updated_at)
		VALUES ('orphan', 'nope', 'missing-version', 'active', 1, 1)`); err != nil {
		t.Fatalf("seeding violation: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("a database with pre-existing violations must still open: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })

	var on int
	if err := reopened.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 0 {
		t.Errorf("foreign_keys = %d; enforcement must stay off when the database already violates it", on)
	}
	// And writes still work.
	if err := reopened.ArchiveSession(t.Context(), "opencode", "s1", 1); err != nil {
		t.Errorf("write failed on a database with pre-existing violations: %v", err)
	}
}

// TestForeignKeyCheckOnRealisticDatabase is the diagnostic the FK
// investigation asked for: build a database that exercises the workflow
// tables the way the app does, then assert the declared constraints are
// actually satisfied. If this ever reports violations, the pragma must
// stay off until the offending write path is fixed.
func TestForeignKeyCheckOnRealisticDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	version, err := db.InsertWorkflowVersion(t.Context(), WorkflowVersion{
		ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1",
		DefinitionJSON: `{"id":"workflow-1","name":"Workflow","version":"1","concurrency":1,` +
			`"nodes":[{"id":"build","name":"Build","type":"command"},{"id":"review","name":"Review","type":"agent"}],` +
			`"dependencies":[{"from":"build","to":"review"}]}`,
		Concurrency: 1, CreatedAt: 1,
		Nodes: []WorkflowNode{
			{ID: "build", Name: "Build", Type: "command"},
			{ID: "review", Name: "Review", Type: "agent", Position: 1},
		},
		Dependencies: []WorkflowDependency{{From: "build", To: "review"}},
	})
	if err != nil {
		t.Fatalf("InsertWorkflowVersion: %v", err)
	}
	if err := db.InsertWorkflowRun(t.Context(), WorkflowRun{
		ID: "run-1", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "active",
		CreatedAt: 1, UpdatedAt: 1,
		Nodes: []WorkflowNodeRun{
			{NodeID: "build", Type: "command", State: "ready"},
			{NodeID: "review", Type: "agent", State: "pending"},
		},
	}); err != nil {
		t.Fatalf("InsertWorkflowRun: %v", err)
	}

	// Rows in the tables with no delete path anywhere in the package.
	if err := db.InsertChildSession(t.Context(), ChildSession{
		ID: "child-1", Platform: "opencode", ParentSessionID: "parent-1",
		Intent: "test", Status: "running", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}
	if err := db.EnqueueMessage(t.Context(), QueuedMessage{
		ID: "q1", Platform: "opencode", SessionID: "parent-1", Text: "hi", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}
	if err := db.ArchiveSession(t.Context(), "opencode", "parent-1", 1); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	if violations := checkForeignKeys(t, db.db); len(violations) != 0 {
		var detail string
		for _, v := range violations {
			detail += fmt.Sprintf("\n  %s -> %s (fk %d, rowid %v)", v.Table, v.Parent, v.FKID, v.RowID)
		}
		t.Fatalf("declared foreign keys are violated by normal writes:%s", detail)
	}
}
