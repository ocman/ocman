package db

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"
)

// extractModelProvider returns the provider and model from a MessageData,
// handling the fallback from top-level fields to nested Model fields.
func extractModelProvider(md MessageData) (provider, model string) {
	provider = md.ProviderID
	model = md.ModelID
	if provider == "" && md.Model != nil {
		provider = md.Model.ProviderID
		model = md.Model.ModelID
	}
	return provider, model
}

// GetStats returns aggregate statistics using SQL aggregation.
func (d *DB) GetStats() (*Stats, error) {
	s := &Stats{}

	err := d.db.QueryRow(`SELECT count(*) FROM session`).Scan(&s.TotalSessions)
	if err != nil {
		return nil, err
	}
	err = d.db.QueryRow(`SELECT count(*) FROM message WHERE json_extract(data, '$.role') = 'user'`).Scan(&s.TotalMessages)
	if err != nil {
		return nil, err
	}
	err = d.db.QueryRow(`SELECT count(DISTINCT directory) FROM session`).Scan(&s.TotalProjects)
	if err != nil {
		return nil, err
	}

	// Aggregate tokens and cost via SQL instead of deserializing every message in Go.
	err = d.db.QueryRow(`
		SELECT
			COALESCE(SUM(COALESCE(json_extract(data, '$.tokens.input'), 0)), 0),
			COALESCE(SUM(COALESCE(json_extract(data, '$.tokens.output'), 0)), 0),
			COALESCE(SUM(COALESCE(json_extract(data, '$.cost'), 0)), 0)
		FROM message
		WHERE json_extract(data, '$.role') = 'assistant'
	`).Scan(&s.TotalTokensIn, &s.TotalTokensOut, &s.TotalCost)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// GetProjects returns all directories with aggregated stats using SQL aggregation.
func (d *DB) GetProjects() ([]ProjectStats, error) {
	rows, err := d.db.Query(`
		SELECT
			s.directory,
			count(s.id) AS session_count,
			max(s.time_updated) AS last_used,
			COALESCE((
				SELECT count(*) FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'user'
			), 0) AS message_count,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(m.data, '$.tokens.input'), 0))
				FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'assistant'
			), 0) AS total_tokens_in,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(m.data, '$.tokens.output'), 0))
				FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'assistant'
			), 0) AS total_tokens_out,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(m.data, '$.cost'), 0))
				FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'assistant'
			), 0) AS total_cost
		FROM session s
		GROUP BY s.directory
		ORDER BY last_used DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ProjectStats
	for rows.Next() {
		var ps ProjectStats
		var lastUsed sql.NullInt64
		err := rows.Scan(
			&ps.Directory,
			&ps.SessionCount,
			&lastUsed,
			&ps.MessageCount,
			&ps.TotalTokensIn,
			&ps.TotalTokensOut,
			&ps.TotalCost,
		)
		if err != nil {
			log.WithError(err).Warn("failed to scan project row")
			continue
		}
		if lastUsed.Valid {
			ps.LastUsed = lastUsed.Int64
		}
		results = append(results, ps)
	}
	if err := rows.Err(); err != nil {
		return results, err
	}
	return results, nil
}

// GetDailyActivity returns activity per day for the last N days.
func (d *DB) GetDailyActivity(days int) ([]DailyActivity, error) {
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()

	// Get session counts per day
	rows, err := d.db.Query(`
		SELECT
			date(time_created / 1000, 'unixepoch', 'localtime') as day,
			count(*) as sessions
		FROM session
		WHERE time_created >= ?
		GROUP BY day
		ORDER BY day
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dayMap := make(map[string]*DailyActivity)
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

	// Get message counts per day
	rows2, err := d.db.Query(`
		SELECT
			date(time_created / 1000, 'unixepoch', 'localtime') as day,
			count(*) as messages
		FROM message
		WHERE time_created >= ?
		GROUP BY day
		ORDER BY day
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var date string
		var messages int
		if err := rows2.Scan(&date, &messages); err != nil {
			log.WithError(err).Warn("failed to scan message activity row")
			continue
		}
		if da, ok := dayMap[date]; ok {
			da.Messages = messages
		} else {
			dayMap[date] = &DailyActivity{Date: date, Messages: messages}
		}
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Fill in gaps and sort
	var result []DailyActivity
	for i := days; i >= 0; i-- {
		d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		if da, ok := dayMap[d]; ok {
			result = append(result, *da)
		} else {
			result = append(result, DailyActivity{Date: d})
		}
	}
	return result, nil
}

// GetModelUsage returns usage stats per model.
func (d *DB) GetModelUsage() ([]ModelUsage, error) {
	rows, err := d.db.Query(`
		SELECT data FROM message
		WHERE json_extract(data, '$.role') = 'assistant'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	modelMap := make(map[string]*ModelUsage)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			log.WithError(err).Warn("failed to scan model usage row")
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			log.WithError(err).Warn("failed to unmarshal message data for model usage")
			continue
		}
		provider, model := extractModelProvider(md)
		if model == "" {
			continue
		}
		key := provider + "/" + model
		mu, ok := modelMap[key]
		if !ok {
			mu = &ModelUsage{Provider: provider, Model: model}
			modelMap[key] = mu
		}
		mu.Count++
		if md.Tokens != nil {
			mu.TokensIn += md.Tokens.Input
			mu.TokensOut += md.Tokens.Output
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]ModelUsage, 0, len(modelMap))
	for _, mu := range modelMap {
		result = append(result, *mu)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Model < result[j].Model
	})
	return result, nil
}

// GetHourlyTokensByModel returns token counts per calendar hour for the last 7 days, broken down by provider/model.
func (d *DB) GetHourlyTokensByModel() ([]HourlyTokensByModel, error) {
	cutoff := time.Now().AddDate(0, 0, -7).UnixMilli()

	rows, err := d.db.Query(`
		SELECT
			strftime('%Y-%m-%d %H', time_created / 1000, 'unixepoch', 'localtime') as datetime,
			data
		FROM message
		WHERE json_extract(data, '$.role') = 'assistant'
		  AND time_created >= ?
	`, cutoff)
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

// GetHourlyActivity returns session counts per hour of day.
func (d *DB) GetHourlyActivity() ([]HourlyActivity, error) {
	rows, err := d.db.Query(`
		SELECT
			cast(strftime('%H', time_created / 1000, 'unixepoch', 'localtime') as integer) as hour,
			count(*) as sessions
		FROM session
		GROUP BY hour
		ORDER BY hour
	`)
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
