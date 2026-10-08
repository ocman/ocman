package state

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutineRunningIndexMigration(t *testing.T) {
	const previousVersion = 112
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = raw.Close() })
			raw.SetMaxOpenConns(1)
			if upgrade {
				if err := ensureSchemaVersionTable(raw); err != nil {
					t.Fatal(err)
				}
				tx, err := raw.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback() }()
				for v := 1; v <= previousVersion; v++ {
					if err := applyMigration(tx, v); err != nil {
						t.Fatalf("apply v%d: %v", v, err)
					}
				}
				if _, err := tx.Exec(`INSERT INTO schema_version VALUES (?, 0)`, previousVersion); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrate(raw); err != nil {
				t.Fatal(err)
			}
			if version, err := currentSchemaVersion(raw); err != nil || version != previousVersion+1 {
				t.Fatalf("schema version = %d, %v; want %d", version, err, previousVersion+1)
			}
			assertRoutineRunningPlan(t, raw)
			if err := migrate(raw); err != nil {
				t.Fatalf("repeat migration: %v", err)
			}
			backups, err := filepath.Glob(path + ".backup-*.db")
			wantBackups := 0
			if upgrade {
				wantBackups = 1
			}
			if err != nil || len(backups) != wantBackups {
				t.Fatalf("backups = %v, %v; want %d", backups, err, wantBackups)
			}
			if upgrade {
				backup, err := sql.Open("sqlite", backups[0]+"?mode=ro")
				if err != nil {
					t.Fatal(err)
				}
				defer backup.Close()
				if version, err := currentSchemaVersion(backup); err != nil || version != previousVersion {
					t.Fatalf("backup version = %d, %v; want %d", version, err, previousVersion)
				}
			}
		})
	}
}

func TestListRunningRoutineRunsQueryPlan(t *testing.T) {
	d := openTestDB(t)
	assertRoutineRunningPlan(t, d.db)
}

func assertRoutineRunningPlan(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT ` + routineRunColumns + ` FROM routine_run WHERE state = 'running' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "; ")
	t.Log(plan)
	if !strings.Contains(plan, "USING INDEX routine_run_running_idx") || strings.Contains(plan, "SCAN routine_run;") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("running routine query must use ordered partial index: %s", plan)
	}
}
