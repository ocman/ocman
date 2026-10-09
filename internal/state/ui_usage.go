package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// UIUsageWindow bounds retry history and prevents delayed/sleeping tabs from
// crediting old time. All intervals are half-open UTC millisecond timestamps.
const UIUsageWindow = 2 * time.Minute

type UIUsageInterval struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type UIUsageDay struct {
	Date          string  `json:"date"`
	ActiveSeconds float64 `json:"activeSeconds"`
}

func migrateUIUsage(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS ui_usage_daily (
		date TEXT PRIMARY KEY, active_ms INTEGER NOT NULL CHECK(active_ms >= 0)
	);
	CREATE TABLE IF NOT EXISTS ui_usage_interval (
		start INTEGER PRIMARY KEY, end INTEGER NOT NULL CHECK(end > start)
	);
	CREATE TABLE IF NOT EXISTS agent_user_wait (
		platform TEXT NOT NULL, session_id TEXT NOT NULL,
		kind TEXT NOT NULL, request_id TEXT NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0, resolved_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(platform, session_id, kind, request_id)
	);`)
	return err
}

// RecordUIUsage unions all clients' intervals in one transaction. Keeping only
// the recent union deduplicates retries and devices without a permanent event log.
func (d *DB) RecordUIUsage(ctx context.Context, intervals []UIUsageInterval, now time.Time) (int64, error) {
	cutoff, until := now.Add(-UIUsageWindow).UnixMilli(), now.UnixMilli()
	if len(intervals) > 64 {
		return 0, errors.New("too many usage intervals")
	}
	for _, interval := range intervals {
		if interval.Start < cutoff || interval.End > until+5_000 || interval.End <= interval.Start {
			return 0, errors.New("usage intervals must fall within the last two minutes")
		}
	}
	if len(intervals) == 0 {
		return 0, nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM ui_usage_interval WHERE end <= ?`, cutoff); err != nil {
		return 0, err
	}
	var credited int64
	for _, interval := range intervals {
		interval.End = min(interval.End, until)
		if interval.End <= interval.Start {
			continue
		}
		rows, err := tx.QueryContext(ctx, `SELECT start, end FROM ui_usage_interval
			WHERE end >= ? AND start <= ? ORDER BY start`, interval.Start, interval.End)
		if err != nil {
			return 0, err
		}
		start, end, cursor := interval.Start, interval.End, interval.Start
		var uncovered []UIUsageInterval
		for rows.Next() {
			var from, to int64
			if err := rows.Scan(&from, &to); err != nil {
				rows.Close()
				return 0, err
			}
			if from > cursor {
				uncovered = append(uncovered, UIUsageInterval{cursor, min(from, interval.End)})
			}
			cursor = max(cursor, to)
			start, end = min(start, from), max(end, to)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if cursor < interval.End {
			uncovered = append(uncovered, UIUsageInterval{cursor, interval.End})
		}
		for _, gap := range uncovered {
			for gap.Start < gap.End {
				day := time.UnixMilli(gap.Start).UTC()
				midnight := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, time.UTC).UnixMilli()
				to := min(gap.End, midnight)
				if _, err := tx.ExecContext(ctx, `INSERT INTO ui_usage_daily(date, active_ms) VALUES (?, ?)
					ON CONFLICT(date) DO UPDATE SET active_ms = active_ms + excluded.active_ms`, day.Format(time.DateOnly), to-gap.Start); err != nil {
					return 0, err
				}
				credited += to - gap.Start
				gap.Start = to
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ui_usage_interval WHERE end >= ? AND start <= ?`, interval.Start, interval.End); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ui_usage_interval(start, end) VALUES (?, ?)`, start, end); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return credited, nil
}

func (d *DB) UIUsageDays(ctx context.Context, since time.Time) ([]UIUsageDay, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT date, active_ms / 1000.0 FROM ui_usage_daily WHERE date >= ? ORDER BY date`, since.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []UIUsageDay{}
	for rows.Next() {
		var day UIUsageDay
		if err := rows.Scan(&day.Date, &day.ActiveSeconds); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}
