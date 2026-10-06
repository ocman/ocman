package state

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// ListFactoryUsageIssues retains phase identity for removed work and avoids
// calculating current dispatch state for historical accounting.
func (d *DB) ListFactoryUsageIssues(ctx context.Context, epicID string) ([]model.NativeIssue, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT i.id, i.kind, COALESCE(w.definition_json, 'null')
		FROM factory_issue i LEFT JOIN factory_workflow_step w ON w.issue_id = i.id
		WHERE i.epic_id = ?`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := []model.NativeIssue{}
	for rows.Next() {
		var issue model.NativeIssue
		var raw string
		if err := rows.Scan(&issue.ID, &issue.Kind, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &issue.Workflow); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}
