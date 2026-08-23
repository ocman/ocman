package db

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"
)

// GetDailyActivity returns activity per day for the last N days.
// GetDailyActivity returns daily session/message counts for the last 365 days,
// optionally filtered by a time window (since), model (modelFilter = "provider/model" or "model"),
// and directory prefix (dir; see directoryWhere).
func (d *DB) GetDailyActivity(ctx context.Context, since int64, modelFilter, dir string) ([]DailyActivity, error) {
	days := 365
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	if since > cutoff {
		cutoff = since
	}

	dirFrag, dirArgs := directoryWhere(dir)

	dayMap := make(map[string]*DailyActivity)

	if modelFilter == "" {
		// No model filter: count all sessions per day (excluding subagent sessions).
		// Alias `session` as `s` so directoryWhere's fragment binds to the same alias.
		query := `
			SELECT
				date(s.time_created / 1000, 'unixepoch', 'localtime') as day,
				count(*) as sessions
			FROM session s
			WHERE s.time_created >= ?
			  AND s.title NOT LIKE '%(% subagent)'`
		args := []interface{}{cutoff}
		if dirFrag != "" {
			query += "\n			  AND " + dirFrag
			args = append(args, dirArgs...)
		}
		query += `
			GROUP BY day
			ORDER BY day
		`
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var da DailyActivity
			if err := rows.Scan(&da.Date, &da.Sessions); err != nil {
				log.WithError(err).Warn("failed to scan daily activity row")
				continue
			}
			dayMap[da.Date] = &da
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Count every assistant turn, including subagents because their model usage
	// and spend are part of the project's activity. Only the model-filtered path
	// needs the message JSON: without a filter we let SQLite do the counting.
	selectExpr, groupBy := "count(*) as messages", "\n		GROUP BY day"
	if modelFilter != "" {
		selectExpr, groupBy = "m.data", ""
	}
	query2 := `
		SELECT
			date(m.time_created / 1000, 'unixepoch', 'localtime') as day,
			` + selectExpr + `
		FROM message m
		JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND m.time_created >= ?`
	args2 := []interface{}{cutoff}
	if dirFrag != "" {
		query2 += "\n		  AND " + dirFrag
		args2 = append(args2, dirArgs...)
	}
	query2 += groupBy + `
		ORDER BY day
	`
	rows2, err := d.db.QueryContext(ctx, query2, args2...)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	addMessages := func(day string, n int) {
		if da, ok := dayMap[day]; ok {
			da.Messages += n
		} else {
			dayMap[day] = &DailyActivity{Date: day, Messages: n}
		}
	}

	for rows2.Next() {
		var day string
		if modelFilter == "" {
			var count int
			if err := rows2.Scan(&day, &count); err != nil {
				log.WithError(err).Warn("failed to scan message activity row")
				continue
			}
			addMessages(day, count)
			continue
		}
		var raw string
		if err := rows2.Scan(&day, &raw); err != nil {
			log.WithError(err).Warn("failed to scan message activity row")
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			continue
		}
		provider, model := extractModelProvider(md)
		if key := modelKey(provider, model); key != modelFilter && model != modelFilter {
			continue
		}
		addMessages(day, 1)
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Count user messages per day (prompts sent by the human).
	query3 := `
		SELECT
			date(m.time_created / 1000, 'unixepoch', 'localtime') as day,
			count(*) as user_messages
		FROM message m
		JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.role') = 'user'
		  AND m.time_created >= ?
		  AND s.title NOT LIKE '%(% subagent)'`
	args3 := []interface{}{cutoff}
	if dirFrag != "" {
		query3 += "\n		  AND " + dirFrag
		args3 = append(args3, dirArgs...)
	}
	query3 += `
		GROUP BY day
		ORDER BY day
	`
	rows3, err := d.db.QueryContext(ctx, query3, args3...)
	if err != nil {
		return nil, err
	}
	defer rows3.Close()

	for rows3.Next() {
		var day string
		var count int
		if err := rows3.Scan(&day, &count); err != nil {
			log.WithError(err).Warn("failed to scan user message activity row")
			continue
		}
		if da, ok := dayMap[day]; ok {
			da.UserMessages = count
		} else {
			dayMap[day] = &DailyActivity{Date: day, UserMessages: count}
		}
	}
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	// Fill in gaps and sort
	var result []DailyActivity
	for i := days; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		if da, ok := dayMap[day]; ok {
			result = append(result, *da)
		} else {
			result = append(result, DailyActivity{Date: day})
		}
	}
	return result, nil
}

// GetHourlyTokensByModel returns token counts per calendar hour, broken down by provider/model.
// windowDays controls how many days back to look (default 7); modelFilter optionally restricts
// to one model key ("provider/model"); dir optionally scopes to a project subtree (see directoryWhere).
func (d *DB) GetHourlyTokensByModel(ctx context.Context, windowDays int, since int64, modelFilter, dir string) ([]HourlyTokensByModel, error) {
	if windowDays <= 0 {
		windowDays = 7
	}
	cutoff := time.Now().AddDate(0, 0, -windowDays).UnixMilli()
	if since > cutoff {
		cutoff = since
	}

	dirFrag, dirArgs := directoryWhere(dir)
	query := `
		SELECT
			strftime('%Y-%m-%d %H', m.time_created / 1000, 'unixepoch', 'localtime') as datetime,
			m.data
		FROM message m`
	if dirFrag != "" {
		query += `
		JOIN session s ON s.id = m.session_id`
	}
	query += `
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND m.time_created >= ?`
	args := []interface{}{cutoff}
	if dirFrag != "" {
		query += "\n		  AND " + dirFrag
		args = append(args, dirArgs...)
	}
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type key struct {
		Datetime string
		Provider string
		Model    string
	}
	agg := make(map[key]*HourlyTokensByModel)

	for rows.Next() {
		var dt string
		var raw string
		if err := rows.Scan(&dt, &raw); err != nil {
			log.WithError(err).Warn("failed to scan hourly tokens row")
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			log.WithError(err).Warn("failed to unmarshal message data for hourly tokens")
			continue
		}
		provider, model := extractModelProvider(md)
		if model == "" {
			continue
		}
		if modelFilter != "" && (provider+"/"+model) != modelFilter && model != modelFilter {
			continue
		}
		k := key{Datetime: dt, Provider: provider, Model: model}
		entry, ok := agg[k]
		if !ok {
			entry = &HourlyTokensByModel{Datetime: dt, Provider: provider, Model: model}
			agg[k] = entry
		}
		if md.Tokens != nil {
			entry.TokensIn += md.Tokens.Input
			entry.TokensOut += md.Tokens.Output
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]HourlyTokensByModel, 0, len(agg))
	for _, v := range agg {
		result = append(result, *v)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Datetime != result[j].Datetime {
			return result[i].Datetime < result[j].Datetime
		}
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Model < result[j].Model
	})
	return result, nil
}

// GetHourlyActivity returns session counts per hour of day, optionally scoped
// to a directory subtree (dir; see directoryWhere).
func (d *DB) GetHourlyActivity(ctx context.Context, since int64, dir string) ([]HourlyActivity, error) {
	dirFrag, dirArgs := directoryWhere(dir)
	query := `
		SELECT
			cast(strftime('%H', s.time_created / 1000, 'unixepoch', 'localtime') as integer) as hour,
			count(*) as sessions
		FROM session s
		WHERE (? <= 0 OR s.time_created >= ?)`
	args := []interface{}{since, since}
	if dirFrag != "" {
		query += "\n		  AND " + dirFrag
		args = append(args, dirArgs...)
	}
	query += `
		GROUP BY hour
		ORDER BY hour
	`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hourMap := make(map[int]int)
	for rows.Next() {
		var hour, count int
		if err := rows.Scan(&hour, &count); err != nil {
			log.WithError(err).Warn("failed to scan hourly activity row")
			continue
		}
		hourMap[hour] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var result []HourlyActivity
	for h := 0; h < 24; h++ {
		result = append(result, HourlyActivity{Hour: h, Sessions: hourMap[h]})
	}
	return result, nil
}
