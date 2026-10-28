package db

import (
	"context"
	"strings"

	"github.com/ocman/ocman/internal/analyticsdata"
)

// effectiveCost prefers reported spend, falling back to API-equivalent cost.
func effectiveCost(reported, estimated float64) float64 {
	if reported > 0 {
		return reported
	}
	return estimated
}

// directoryWhere scopes queries to a directory and its descendants, without
// including sibling prefixes such as /repo/foobar when filtering /repo/foo.
func directoryWhere(dir string) (string, []interface{}) {
	if dir == "" {
		return "", nil
	}
	return "(s.directory = ? OR s.directory LIKE ?)", []interface{}{dir, dir + "/%"}
}

// GetNewAssistantMessages returns assistant rows after the scanner's high-water
// mark. Analytics facts and counters come from the same retained projection.
func (d *DB) GetNewAssistantMessages(ctx context.Context, since int64) ([]LLMMessageRow, int64, error) {
	rows, err := d.reads().QueryContext(ctx, `SELECT m.time_created,m.session_id,m.provider,m.model,m.cost,
		m.input_tokens,m.output_tokens,m.cache_read,m.cache_write,m.finish,m.duration_ms
		FROM history_fact m JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
		WHERE m.role='assistant' AND m.valid=1 AND m.time_created>? ORDER BY m.time_created,m.id`, since)
	if err != nil {
		return nil, since, err
	}
	defer rows.Close()
	var result []LLMMessageRow
	hwm := since
	for rows.Next() {
		var row LLMMessageRow
		var provider, model string
		if err := rows.Scan(&row.TimeCreated, &row.SessionID, &provider, &model, &row.Cost,
			&row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens, &row.StopReason, &row.DurationMs); err != nil {
			return nil, since, err
		}
		row.Model = analyticsdata.ModelKey(provider, model)
		row.StopReason = strings.TrimSpace(row.StopReason)
		if row.StopReason == "" {
			row.StopReason = "none"
		}
		result = append(result, row)
		hwm = max(hwm, row.TimeCreated)
	}
	if err := rows.Err(); err != nil {
		return nil, since, err
	}
	return result, hwm, nil
}

// GetMaxMessageTime initializes the scanner without re-emitting old metrics.
func (d *DB) GetMaxMessageTime(ctx context.Context) (int64, error) {
	var maxTime int64
	err := d.reads().QueryRowContext(ctx, `SELECT COALESCE(MAX(m.time_created), 0)
		FROM history_fact m JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id`).Scan(&maxTime)
	return maxTime, err
}
