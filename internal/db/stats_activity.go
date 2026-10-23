package db

import "context"

// GetDailyActivity returns retained activity over the past year.
func (d *DB) GetDailyActivity(ctx context.Context, since int64, modelFilter, dir string) ([]DailyActivity, error) {
	return d.historyDailyActivity(ctx, since, modelFilter, dir)
}

// GetHourlyTokensByModel returns retained token counts by calendar hour.
func (d *DB) GetHourlyTokensByModel(ctx context.Context, windowDays int, since int64, modelFilter, dir string) ([]HourlyTokensByModel, error) {
	return d.historyHourlyTokens(ctx, windowDays, since, modelFilter, dir)
}

// GetHourlyActivity returns session counts per hour of day.
func (d *DB) GetHourlyActivity(ctx context.Context, since int64, dir string) ([]HourlyActivity, error) {
	query := `SELECT cast(strftime('%H', s.time_created / 1000, 'unixepoch', 'localtime') as integer), count(*)
		FROM session s WHERE (? <= 0 OR s.time_created >= ?)`
	args := []interface{}{since, since}
	if clause, values := directoryWhere(dir); clause != "" {
		query += ` AND ` + clause
		args = append(args, values...)
	}
	query += ` GROUP BY 1 ORDER BY 1`
	rows, err := d.reads().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]HourlyActivity, 24)
	for hour := range result {
		result[hour].Hour = hour
	}
	for rows.Next() {
		var hour, count int
		if err := rows.Scan(&hour, &count); err != nil {
			return nil, err
		}
		result[hour].Sessions = count
	}
	return result, rows.Err()
}
