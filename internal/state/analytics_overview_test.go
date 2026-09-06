package state

import (
	"reflect"
	"testing"
)

func TestAnalyticsOverviewCounts(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	statements := []string{
		`INSERT INTO routine (id, name, prompt, directory, remote_id, schedule_kind, schedule_config_json, enabled, deleted, created_at, updated_at, deleted_at) VALUES
			('active', 'Active', '', '/repo', 'local', 'manual', '{}', 0, 0, 1, 1, 0),
			('deleted', 'Deleted', '', '/repo', 'local', 'manual', '{}', 0, 1, 1, 2, 2)`,
		`INSERT INTO routine_run (id, routine_id, routine_updated_at, routine_name, prompt, directory, remote_id, trigger, state, occurrence_at, created_at) VALUES
			('run-1', 'active', 1, 'Active', '', '/repo', 'local', 'manual', 'running', 1, 1),
			('run-2', 'active', 1, 'Active', '', '/repo', 'local', 'manual', 'done', 2, 2),
			('run-3', 'deleted', 2, 'Deleted', '', '/repo', 'local', 'manual', 'done', 3, 3)`,
		`INSERT INTO factory_project (path, created_at) VALUES ('/repo', 1)`,
		`INSERT INTO factory_epic (id, project_path, status, goal, created_at, updated_at) VALUES
			('epic-open', '/repo', 'open', 'Open', 1, 1),
			('epic-closed', '/repo', 'closed', 'Closed', 1, 1)`,
		`INSERT INTO factory_issue (id, epic_id, kind, title, status, created_at) VALUES
			('issue-open', 'epic-open', 'implementation', 'Open', 'open', 1),
			('issue-closed', 'epic-open', 'implementation', 'Closed', 'closed', 1),
			('issue-removed', 'epic-open', 'implementation', 'Removed', 'open', 1)`,
		`INSERT INTO factory_removed_issue (issue_id, plan_id, plan_revision, removed_at) VALUES ('issue-removed', 'epic-open', 1, 2)`,
		`INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, terminal_outcome, frozen_policy_json, created_at, updated_at) VALUES
			('prepared', 'epic-open', 'work-1', 1, 'prepared', '', '{}', 1, 1),
			('active', 'epic-open', 'work-2', 1, 'active', '', '{}', 1, 1),
			('succeeded-1', 'epic-open', 'work-3', 1, 'terminal', 'succeeded', '{}', 1, 1),
			('succeeded-2', 'epic-open', 'work-4', 1, 'terminal', 'succeeded', '{}', 1, 1),
			('failed', 'epic-open', 'work-5', 1, 'terminal', 'failed', '{}', 1, 1)`,
	}
	for _, statement := range statements {
		if _, err := db.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.AnalyticsOverviewCounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := AnalyticsOverviewCounts{
		TotalRoutines:                    1,
		RoutineRunsByStatus:              map[string]int{"done": 2, "running": 1},
		FactoryEpicsByStatus:             map[string]int{"closed": 1, "open": 1},
		FactoryIssuesByStatus:            map[string]int{"closed": 1, "open": 1},
		FactoryAttemptsByPhase:           map[string]int{"active": 1, "prepared": 1, "terminal": 3},
		FactoryAttemptsByTerminalOutcome: map[string]int{"failed": 1, "succeeded": 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %#v, want %#v", got, want)
	}
}
