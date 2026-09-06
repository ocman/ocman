package state

import "context"

type AnalyticsOverviewCounts struct {
	TotalRoutines                    int            `json:"totalRoutines"`
	RoutineRunsByStatus              map[string]int `json:"routineRunsByStatus"`
	FactoryEpicsByStatus             map[string]int `json:"factoryEpicsByStatus"`
	FactoryIssuesByStatus            map[string]int `json:"factoryIssuesByStatus"`
	FactoryAttemptsByPhase           map[string]int `json:"factoryAttemptsByPhase"`
	FactoryAttemptsByTerminalOutcome map[string]int `json:"factoryAttemptsByTerminalOutcome"`
}

func (d *DB) AnalyticsOverviewCounts(ctx context.Context) (AnalyticsOverviewCounts, error) {
	counts := AnalyticsOverviewCounts{
		RoutineRunsByStatus:              map[string]int{},
		FactoryEpicsByStatus:             map[string]int{},
		FactoryIssuesByStatus:            map[string]int{},
		FactoryAttemptsByPhase:           map[string]int{},
		FactoryAttemptsByTerminalOutcome: map[string]int{},
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT 'total_routines', '', count(*) FROM routine WHERE deleted = 0
		UNION ALL SELECT 'routine_runs', state, count(*) FROM routine_run GROUP BY state
		UNION ALL SELECT 'factory_epics', status, count(*) FROM factory_epic GROUP BY status
		UNION ALL SELECT 'factory_issues', status, count(*) FROM factory_issue
			WHERE NOT EXISTS (SELECT 1 FROM factory_removed_issue r WHERE r.issue_id = factory_issue.id)
			GROUP BY status
		UNION ALL SELECT 'factory_attempt_phases', phase, count(*) FROM factory_attempt GROUP BY phase
		UNION ALL SELECT 'factory_attempt_outcomes', terminal_outcome, count(*) FROM factory_attempt
			WHERE phase = 'terminal' GROUP BY terminal_outcome
	`)
	if err != nil {
		return counts, err
	}
	defer rows.Close()

	for rows.Next() {
		var metric, key string
		var count int
		if err := rows.Scan(&metric, &key, &count); err != nil {
			return counts, err
		}
		switch metric {
		case "total_routines":
			counts.TotalRoutines = count
		case "routine_runs":
			counts.RoutineRunsByStatus[key] = count
		case "factory_epics":
			counts.FactoryEpicsByStatus[key] = count
		case "factory_issues":
			counts.FactoryIssuesByStatus[key] = count
		case "factory_attempt_phases":
			counts.FactoryAttemptsByPhase[key] = count
		case "factory_attempt_outcomes":
			counts.FactoryAttemptsByTerminalOutcome[key] = count
		}
	}
	return counts, rows.Err()
}
