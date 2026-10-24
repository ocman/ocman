package db

import (
	"context"
	"sort"
	"time"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

// GetDailyActivity returns retained activity over the past year.
func (d *DB) GetDailyActivity(ctx context.Context, since int64, model, dir string) ([]DailyActivity, error) {
	now := time.Now()
	cutoff := max(since, now.AddDate(0, 0, -365).UnixMilli())
	days := map[string]DailyActivity{}
	if model == "" {
		query := `SELECT date(s.time_created/1000,'unixepoch','localtime'),count(*) FROM session s WHERE s.time_created>=? AND s.is_subagent=0`
		args := []any{cutoff}
		if clause, values := directoryWhere(dir); clause != "" {
			query += ` AND ` + clause
			args = append(args, values...)
		}
		query += ` GROUP BY 1`
		rows, err := d.reads().QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a DailyActivity
			if err := rows.Scan(&a.Date, &a.Sessions); err != nil {
				rows.Close()
				return nil, err
			}
			days[a.Date] = a
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, role := range []string{"assistant", "user"} {
		rows, err := d.historyBuckets(ctx, cutoff, role, dir, "%Y-%m-%d", role == "assistant" && model != "")
		if err != nil {
			return nil, err
		}
		for _, b := range rows {
			if role == "assistant" && model != "" && model != b.model && model != analyticsdata.ModelKey(b.provider, b.model) {
				continue
			}
			a := days[b.label]
			a.Date = b.label
			if role == "assistant" {
				a.Messages += b.messages
			} else {
				a.UserMessages += b.messages
			}
			days[b.label] = a
		}
	}
	result := make([]DailyActivity, 0, 366)
	for i := 365; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		a := days[day]
		a.Date = day
		result = append(result, a)
	}
	return result, nil
}

// GetModelUsage aggregates retained usage, scoped by time and directory.
func (d *DB) GetModelUsage(ctx context.Context, since int64, dir string) ([]ModelUsage, error) {
	rows, err := d.historyBuckets(ctx, since, "assistant", dir, "", true)
	if err != nil {
		return nil, err
	}
	models := map[string]*ModelUsage{}
	for _, b := range rows {
		if b.model == "" {
			continue
		}
		key := analyticsdata.ModelKey(b.provider, b.model)
		m := models[key]
		if m == nil {
			m = &ModelUsage{Provider: b.provider, Model: b.model}
			models[key] = m
		}
		m.Count += b.messages
		m.TokensIn += b.input
		m.TokensOut += b.output
		m.CacheRead += b.read
		m.CacheWrite += b.write
	}
	result := make([]ModelUsage, 0, len(models))
	for _, m := range models {
		result = append(result, *m)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Model < result[j].Model
	})
	return result, nil
}

// GetHourlyTokensByModel returns retained token counts by calendar hour.
func (d *DB) GetHourlyTokensByModel(ctx context.Context, window int, since int64, model, dir string) ([]HourlyTokensByModel, error) {
	if window <= 0 {
		window = 7
	}
	cutoff := max(since, time.Now().AddDate(0, 0, -window).UnixMilli())
	rows, err := d.historyBuckets(ctx, cutoff, "assistant", dir, "%Y-%m-%d %H", true)
	if err != nil {
		return nil, err
	}
	result := make([]HourlyTokensByModel, 0, len(rows))
	for _, b := range rows {
		if b.model == "" || (model != "" && model != b.model && model != b.provider+"/"+b.model) {
			continue
		}
		result = append(result, HourlyTokensByModel{Datetime: b.label, Provider: b.provider, Model: b.model, TokensIn: b.input, TokensOut: b.output})
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Datetime != b.Datetime {
			return a.Datetime < b.Datetime
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Model < b.Model
	})
	return result, nil
}
