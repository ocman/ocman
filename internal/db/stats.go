package db

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
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

// CostCalculator can compute the API-equivalent cost for a request given token counts.
// Pass nil to skip calculated-cost population.
type CostCalculator interface {
	CalcCost(modelID string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64
}

// GetMetricsDashboard returns filtered request-level analytics for the dashboard.
// days is the number of days in the selected window (0 = all time); it drives bucket granularity.
// pricing may be nil, in which case CalcCost fields are left zero.
func (d *DB) GetMetricsDashboard(agentFilter, modelFilter string, since int64, days, requestLimit, requestOffset int, pricing CostCalculator) (*MetricsDashboard, error) {
	rows, err := d.db.Query(`
		SELECT m.id, m.session_id, m.time_created, m.data
		FROM message m
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND (? <= 0 OR m.time_created >= ?)
		ORDER BY m.time_created ASC
	`, since, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type requestRow struct {
		RequestLogEntry
	}

	dashboard := &MetricsDashboard{}
	agentSet := make(map[string]struct{})
	modelSet := make(map[string]struct{})
	stopCounts := make(map[string]int)
	filtered := make([]requestRow, 0)

	for rows.Next() {
		var id, sessionID string
		var timeCreated int64
		var raw string
		if err := rows.Scan(&id, &sessionID, &timeCreated, &raw); err != nil {
			log.WithError(err).Warn("failed to scan metrics dashboard row")
			continue
		}

		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			log.WithError(err).Warn("failed to unmarshal metrics dashboard message")
			continue
		}

		agent := strings.TrimSpace(md.Agent)
		provider, model := extractModelProvider(md)
		modelKey := strings.TrimSpace(model)
		if provider != "" && modelKey != "" {
			modelKey = provider + "/" + modelKey
		}

		if agent != "" {
			agentSet[agent] = struct{}{}
		}
		if modelKey != "" {
			modelSet[modelKey] = struct{}{}
		}

		if agentFilter != "" && agent != agentFilter {
			continue
		}
		if modelFilter != "" && modelKey != modelFilter {
			continue
		}

		entry := requestRow{RequestLogEntry: RequestLogEntry{
			ID:          id,
			SessionID:   sessionID,
			TimeCreated: timeCreated,
			Agent:       agent,
			Model:       modelKey,
			Cost:        md.Cost,
		}}
		if md.Tokens != nil {
			entry.InputTokens = md.Tokens.Input
			entry.OutputTokens = md.Tokens.Output
			if md.Tokens.Cache != nil {
				entry.CacheReadTokens = md.Tokens.Cache.Read
				entry.CacheWriteTokens = md.Tokens.Cache.Write
			}
		}
		if md.Time != nil && md.Time.Completed > md.Time.Created {
			entry.DurationMs = md.Time.Completed - md.Time.Created
		}
		if entry.DurationMs > 0 {
			entry.TokensPerSecond = float64(entry.OutputTokens) / (float64(entry.DurationMs) / 1000)
		}
		entry.StopReason = strings.TrimSpace(md.Finish)
		if entry.StopReason == "" {
			entry.StopReason = "none"
		}
		if pricing != nil {
			entry.CalcCost = pricing.CalcCost(modelKey, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, entry.CacheWriteTokens)
		}

		filtered = append(filtered, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dashboard.AvailableAgents = sortedKeys(agentSet)
	dashboard.AvailableModels = sortedKeys(modelSet)

	// Choose bucket granularity: hourly for short windows, daily otherwise.
	hourly := days > 0 && days <= 7
	bucketFmt := "2006-01-02"
	if hourly {
		bucketFmt = "2006-01-02 15"
	}

	type bucketAcc struct {
		label             string
		inputTokens       int64
		cacheReadTokens   int64
		cacheWriteTokens  int64
		outputTokens      int64
		totalOutputTokSec float64
		totalDurationMs   float64
		totalCacheEff     float64
		totalCost         float64
		durationCount     int
		count             int
	}
	bucketOrder := make([]string, 0)
	buckets := make(map[string]*bucketAcc)

	validDurationCount := 0
	for _, entry := range filtered {
		totalTokens := entry.InputTokens + entry.OutputTokens
		dashboard.Summary.Requests++
		dashboard.Summary.TotalTokens += totalTokens
		dashboard.Summary.InputTokens += entry.InputTokens
		dashboard.Summary.OutputTokens += entry.OutputTokens
		dashboard.Summary.CacheReadTokens += entry.CacheReadTokens
		dashboard.Summary.CacheWriteTokens += entry.CacheWriteTokens
		dashboard.Summary.TotalCost += entry.Cost
		dashboard.Summary.TotalCalcCost += entry.CalcCost
		if entry.DurationMs > 0 {
			dashboard.Summary.AvgDurationMs += float64(entry.DurationMs)
			dashboard.Summary.AvgTokensPerSec += entry.TokensPerSecond
			validDurationCount++
		}
		stopCounts[entry.StopReason]++

		label := time.UnixMilli(entry.TimeCreated).Local().Format(bucketFmt)
		b, ok := buckets[label]
		if !ok {
			b = &bucketAcc{label: label}
			buckets[label] = b
			bucketOrder = append(bucketOrder, label)
		}
		b.inputTokens += entry.InputTokens
		b.cacheReadTokens += entry.CacheReadTokens
		b.cacheWriteTokens += entry.CacheWriteTokens
		b.outputTokens += entry.OutputTokens
		b.totalOutputTokSec += entry.TokensPerSecond
		if entry.DurationMs > 0 {
			b.totalDurationMs += float64(entry.DurationMs)
			b.durationCount++
		}
		cacheEff := 0.0
		if tc := entry.CacheReadTokens + entry.CacheWriteTokens; tc > 0 {
			cacheEff = float64(entry.CacheReadTokens) / float64(tc)
		}
		b.totalCacheEff += cacheEff
		b.totalCost += entry.Cost
		b.count++
	}

	if validDurationCount > 0 {
		dashboard.Summary.AvgDurationMs /= float64(validDurationCount)
		dashboard.Summary.AvgTokensPerSec /= float64(validDurationCount)
	}
	if totalCache := dashboard.Summary.CacheReadTokens + dashboard.Summary.CacheWriteTokens; totalCache > 0 {
		dashboard.Summary.CacheHitRate = float64(dashboard.Summary.CacheReadTokens) / float64(totalCache)
	}

	// Build the series with gap-filling: enumerate every bucket in the window so
	// empty days/hours are represented as zero-valued points.
	var seriesLabels []string
	if days > 0 {
		start := time.UnixMilli(since).Local()
		now := time.Now().Local()
		if hourly {
			// Truncate to the hour and walk forward hour by hour.
			cur := start.Truncate(time.Hour)
			for !cur.After(now) {
				seriesLabels = append(seriesLabels, cur.Format(bucketFmt))
				cur = cur.Add(time.Hour)
			}
		} else {
			// Truncate to the day and walk forward day by day.
			cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
			for !cur.After(now) {
				seriesLabels = append(seriesLabels, cur.Format(bucketFmt))
				cur = cur.AddDate(0, 0, 1)
			}
		}
	} else {
		// All-time: fall back to only the buckets that have data.
		seriesLabels = bucketOrder
	}

	cumCost := 0.0
	for _, label := range seriesLabels {
		b := buckets[label] // nil for empty buckets
		var (
			totalCost         float64
			totalOutputTokSec float64
			totalCacheEff     float64
			totalDurationMs   float64
			inputTokens       int64
			cacheReadTokens   int64
			outputTokens      int64
			durationCount     int
			count             int
		)
		if b != nil {
			totalCost = b.totalCost
			totalOutputTokSec = b.totalOutputTokSec
			totalCacheEff = b.totalCacheEff
			totalDurationMs = b.totalDurationMs
			inputTokens = b.inputTokens
			cacheReadTokens = b.cacheReadTokens
			outputTokens = b.outputTokens
			durationCount = b.durationCount
			count = b.count
		}
		cumCost += totalCost
		avgDur := 0.0
		if durationCount > 0 {
			avgDur = totalDurationMs / float64(durationCount)
		}
		n := count
		if n < 1 {
			n = 1
		}
		dashboard.Series = append(dashboard.Series, MetricsPoint{
			Label:              label,
			AvgOutputTokensSec: totalOutputTokSec / float64(n),
			CumulativeCost:     cumCost,
			InputTokens:        inputTokens,
			CacheReadTokens:    cacheReadTokens,
			OutputTokens:       outputTokens,
			AvgDurationMs:      avgDur,
			AvgCacheEfficiency: totalCacheEff / float64(n),
			Count:              count,
		})
	}

	for reason, count := range stopCounts {
		dashboard.StopReasons = append(dashboard.StopReasons, StopReasonCount{Reason: reason, Count: count})
	}
	sort.Slice(dashboard.StopReasons, func(i, j int) bool {
		if dashboard.StopReasons[i].Count != dashboard.StopReasons[j].Count {
			return dashboard.StopReasons[i].Count > dashboard.StopReasons[j].Count
		}
		return dashboard.StopReasons[i].Reason < dashboard.StopReasons[j].Reason
	})

	dashboard.TotalRequests = len(filtered)

	// Reverse so most-recent is first, then apply offset + limit.
	start := requestOffset
	if start < 0 {
		start = 0
	}
	if start > len(filtered) {
		start = len(filtered)
	}
	end := len(filtered) - start
	page := filtered[:end]
	if requestLimit > 0 && len(page) > requestLimit {
		page = page[len(page)-requestLimit:]
	}
	for i := len(page) - 1; i >= 0; i-- {
		dashboard.Requests = append(dashboard.Requests, page[i].RequestLogEntry)
	}

	return dashboard, nil
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
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
// GetDailyActivity returns daily session/message counts for the last 365 days,
// optionally filtered by a time window (since) and model (modelFilter = "provider/model" or "model").
func (d *DB) GetDailyActivity(since int64, modelFilter string) ([]DailyActivity, error) {
	days := 365
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	if since > cutoff {
		cutoff = since
	}

	dayMap := make(map[string]*DailyActivity)

	if modelFilter == "" {
		// No model filter: count all sessions per day (excluding subagent sessions).
		rows, err := d.db.Query(`
			SELECT
				date(time_created / 1000, 'unixepoch', 'localtime') as day,
				count(*) as sessions
			FROM session
			WHERE time_created >= ?
			  AND title NOT LIKE '%(% subagent)'
			GROUP BY day
			ORDER BY day
		`, cutoff)
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

	// Count assistant messages per day, excluding messages from subagent sessions.
	rows2, err := d.db.Query(`
		SELECT
			date(m.time_created / 1000, 'unixepoch', 'localtime') as day,
			m.data
		FROM message m
		JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND m.time_created >= ?
		  AND s.title NOT LIKE '%(% subagent)'
		ORDER BY day
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var day string
		var raw string
		if err := rows2.Scan(&day, &raw); err != nil {
			log.WithError(err).Warn("failed to scan message activity row")
			continue
		}
		if modelFilter != "" {
			var md MessageData
			if err := json.Unmarshal([]byte(raw), &md); err != nil {
				continue
			}
			provider, model := extractModelProvider(md)
			key := provider + "/" + model
			if key != modelFilter && model != modelFilter {
				continue
			}
		}
		if da, ok := dayMap[day]; ok {
			da.Messages++
		} else {
			dayMap[day] = &DailyActivity{Date: day, Messages: 1}
		}
	}
	if err := rows2.Err(); err != nil {
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

// GetModelUsage returns usage stats per model, optionally filtered by time window.
func (d *DB) GetModelUsage(since int64) ([]ModelUsage, error) {
	rows, err := d.db.Query(`
		SELECT data FROM message
		WHERE json_extract(data, '$.role') = 'assistant'
		  AND (? <= 0 OR time_created >= ?)
	`, since, since)
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

// GetHourlyTokensByModel returns token counts per calendar hour, broken down by provider/model.
// windowDays controls how many days back to look (default 7); modelFilter optionally restricts to one model key ("provider/model").
func (d *DB) GetHourlyTokensByModel(windowDays int, since int64, modelFilter string) ([]HourlyTokensByModel, error) {
	if windowDays <= 0 {
		windowDays = 7
	}
	cutoff := time.Now().AddDate(0, 0, -windowDays).UnixMilli()
	if since > cutoff {
		cutoff = since
	}

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

// GetHourlyActivity returns session counts per hour of day.
func (d *DB) GetHourlyActivity(since int64) ([]HourlyActivity, error) {
	rows, err := d.db.Query(`
		SELECT
			cast(strftime('%H', time_created / 1000, 'unixepoch', 'localtime') as integer) as hour,
			count(*) as sessions
		FROM session
		WHERE (? <= 0 OR time_created >= ?)
		GROUP BY hour
		ORDER BY hour
	`, since, since)
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
