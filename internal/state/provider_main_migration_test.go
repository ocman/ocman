package state

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProviderMainMigrationReconcilesBothHistories(t *testing.T) {
	for _, history := range []string{"main", "provider"} {
		t.Run(history, func(t *testing.T) {
			raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
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
			defer func() { _ = tx.Rollback() }()
			version := 121
			if history == "provider" {
				version = 111
			}
			for v := 1; v <= version; v++ {
				if err := applyMigration(tx, v); err != nil {
					t.Fatalf("v%d: %v", v, err)
				}
			}
			if history == "provider" {
				if err := migrateToV112(tx); err != nil {
					t.Fatal(err)
				}
				if err := migrateToV113(tx); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(`INSERT INTO routine (id, name, prompt, directory, remote_id, agent, model, session_mode, session_id, schedule_kind, schedule_config_json, permission_rules_json, next_due_at, enabled, deleted, delete_after_success, created_at, updated_at, platform) VALUES ('r', 'r', 'p', '/repo', 'local', '', '', 'new', '', 'none', '{}', '[]', 0, 1, 0, 0, 1, 1, 'claudecode')`); err != nil {
					t.Fatal(err)
				}
				version = 113
			}
			if _, err := tx.Exec(`INSERT INTO schema_version VALUES (?, 0)`, version); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := migrate(raw); err != nil {
				t.Fatal(err)
			}
			if err := migrate(raw); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{
				`SELECT interrupted FROM seen_session LIMIT 0`,
				`SELECT platform, worktree, cleanup_worktree FROM routine LIMIT 0`,
				`SELECT platform FROM factory_epic LIMIT 0`,
				`SELECT endpoint FROM managed_provider LIMIT 0`,
			} {
				rows, err := raw.Query(query)
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
			assertRoutineRunningPlan(t, raw)
			if history == "provider" {
				var platform string
				if err := raw.QueryRow(`SELECT platform FROM routine WHERE id='r'`).Scan(&platform); err != nil || platform != "claudecode" {
					t.Fatalf("platform=%q: %v", platform, err)
				}
			}
		})
	}
}
