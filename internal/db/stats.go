package db

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// directoryWhere builds a SQL fragment that scopes a query to a project
// subtree. The returned fragment is intended to be AND'd into an existing
// WHERE clause; the args go into the same parameter list.
//
// Behaviour (see spec/stats-project-filter/architecture.md, AD-7):
//
//   - Empty `dir` returns `("", nil)`. Callers append the fragment only when
//     non-empty so the unfiltered query plan is unchanged.
//   - Non-empty `dir` returns the two-predicate form
//     `(s.directory = ? OR s.directory LIKE ?)` with args `[dir, dir+"/%"]`.
//
// The two-predicate form (rather than a single `LIKE dir||'%'`) is what
// prevents the sibling-prefix trap: scope `/repo/foo` must NOT match
// `/repo/foobar`. Tested explicitly in stats_dir_filter_test.go.
//
// The fragment uses the alias `s` for the session table; queries that don't
// already alias their session join `s` should do so before applying it.
func directoryWhere(dir string) (string, []interface{}) {
	if dir == "" {
		return "", nil
	}
	return "(s.directory = ? OR s.directory LIKE ?)", []interface{}{dir, dir + "/%"}
}

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

// requestRow is the internal shape used while aggregating metrics. It wraps
// RequestLogEntry so we can attach computed fields without mutating the API
// type.
type requestRow struct {
	RequestLogEntry
}

// GetMetricsDashboard returns filtered request-level analytics for the dashboard.
// days is the number of days in the selected window (0 = all time); it drives bucket granularity.
// pricing may be nil, in which case CalcCost fields are left zero.
// sessionLimit/sessionOffset control pagination of the Sessions aggregation (most-recent activity first).
// dir, when non-empty, scopes the query to sessions whose directory equals dir or starts with dir+"/" (see directoryWhere).
func (d *DB) GetMetricsDashboard(agentFilter, modelFilter string, since int64, days, requestLimit, requestOffset, sessionLimit, sessionOffset, projectLimit, projectOffset int, pricing CostCalculator, dir string) (*MetricsDashboard, error) {
	dirFrag, dirArgs := directoryWhere(dir)
	query := `
		SELECT m.id, m.session_id, m.time_created, m.data
		FROM message m`
	if dirFrag != "" {
		// Join only when scoping; preserves the existing query plan for the
		// unfiltered case.
		query += `
		JOIN session s ON s.id = m.session_id`
	}
	query += `
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND (? <= 0 OR m.time_created >= ?)`
	args := []interface{}{since, since}
	if dirFrag != "" {
		query += "\n		  AND " + dirFrag
		args = append(args, dirArgs...)
	}
	query += `
		ORDER BY m.time_created ASC
	`
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
		totalCalcCost     float64
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
			dashboard.Summary.TotalDurationMs += entry.DurationMs
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
		b.totalCalcCost += entry.CalcCost
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
	cumCalcCost := 0.0
	for _, label := range seriesLabels {
		b := buckets[label] // nil for empty buckets
		var (
			totalCost         float64
			totalCalcCost     float64
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
			totalCalcCost = b.totalCalcCost
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
		cumCalcCost += totalCalcCost
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
			CumulativeCalcCost: cumCalcCost,
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

	// ------------------------------------------------------------------
	// Session log: aggregate the same filtered requests by session id.
	// ------------------------------------------------------------------
	if err := d.populateSessionLog(dashboard, filtered, sessionLimit, sessionOffset); err != nil {
		return nil, err
	}

	// ------------------------------------------------------------------
	// Project log: aggregate the same filtered requests by directory.
	// ------------------------------------------------------------------
	if err := d.populateProjectLog(dashboard, filtered, projectLimit, projectOffset); err != nil {
		return nil, err
	}

	return dashboard, nil
}

// populateSessionLog aggregates the already-filtered request rows by session id
// and applies pagination. Session metadata (title, directory) is fetched from
// the session table in a single query.
func (d *DB) populateSessionLog(dashboard *MetricsDashboard, filtered []requestRow, sessionLimit, sessionOffset int) error {
	if len(filtered) == 0 {
		return nil
	}

	type sessionAcc struct {
		entry          SessionLogEntry
		agentSet       map[string]struct{}
		modelSet       map[string]struct{}
		tokPerSecTotal float64
		durationCount  int
	}
	accs := make(map[string]*sessionAcc)
	order := make([]string, 0)

	for _, entry := range filtered {
		acc, ok := accs[entry.SessionID]
		if !ok {
			acc = &sessionAcc{
				entry:    SessionLogEntry{ID: entry.SessionID, FirstRequestTime: entry.TimeCreated, LastRequestTime: entry.TimeCreated},
				agentSet: make(map[string]struct{}),
				modelSet: make(map[string]struct{}),
			}
			accs[entry.SessionID] = acc
			order = append(order, entry.SessionID)
		}
		acc.entry.Requests++
		acc.entry.InputTokens += entry.InputTokens
		acc.entry.OutputTokens += entry.OutputTokens
		acc.entry.CacheReadTokens += entry.CacheReadTokens
		acc.entry.CacheWriteTokens += entry.CacheWriteTokens
		acc.entry.TotalTokens += entry.InputTokens + entry.OutputTokens
		acc.entry.TotalDurationMs += entry.DurationMs
		acc.entry.Cost += entry.Cost
		acc.entry.CalcCost += entry.CalcCost
		if entry.DurationMs > 0 {
			acc.tokPerSecTotal += entry.TokensPerSecond
			acc.durationCount++
		}
		if entry.TimeCreated < acc.entry.FirstRequestTime {
			acc.entry.FirstRequestTime = entry.TimeCreated
		}
		if entry.TimeCreated > acc.entry.LastRequestTime {
			acc.entry.LastRequestTime = entry.TimeCreated
		}
		if entry.Agent != "" {
			acc.agentSet[entry.Agent] = struct{}{}
		}
		if entry.Model != "" {
			acc.modelSet[entry.Model] = struct{}{}
		}
		if entry.StopReason == "error" {
			acc.entry.ErrorCount++
		}
	}

	// Look up session titles/directories in a single query.
	ids := make([]string, 0, len(accs))
	for id := range accs {
		ids = append(ids, id)
	}
	titles, dirs, err := d.lookupSessionMetadata(ids)
	if err != nil {
		return err
	}

	entries := make([]SessionLogEntry, 0, len(accs))
	for _, id := range order {
		acc := accs[id]
		if acc.durationCount > 0 {
			acc.entry.AvgTokensPerSec = acc.tokPerSecTotal / float64(acc.durationCount)
		}
		acc.entry.Agents = sortedKeys(acc.agentSet)
		acc.entry.Models = sortedKeys(acc.modelSet)
		acc.entry.Title = titles[id]
		acc.entry.Directory = dirs[id]
		entries = append(entries, acc.entry)
	}

	// Sort by most-recent activity first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastRequestTime > entries[j].LastRequestTime
	})

	dashboard.TotalSessions = len(entries)

	// Apply offset + limit.
	offset := sessionOffset
	if offset < 0 {
		offset = 0
	}
	if offset > len(entries) {
		offset = len(entries)
	}
	paged := entries[offset:]
	if sessionLimit > 0 && len(paged) > sessionLimit {
		paged = paged[:sessionLimit]
	}
	dashboard.Sessions = paged
	return nil
}

// populateProjectLog aggregates the already-filtered request rows by project
// directory and applies pagination. Directories are resolved from session
// metadata in a single query. Sorted by most-recent activity first.
func (d *DB) populateProjectLog(dashboard *MetricsDashboard, filtered []requestRow, projectLimit, projectOffset int) error {
	if len(filtered) == 0 {
		return nil
	}

	// Collect the unique session IDs so we can look up directories.
	sessionIDs := make([]string, 0)
	seenSessions := make(map[string]struct{})
	for _, r := range filtered {
		if _, ok := seenSessions[r.SessionID]; !ok {
			seenSessions[r.SessionID] = struct{}{}
			sessionIDs = append(sessionIDs, r.SessionID)
		}
	}
	_, dirs, err := d.lookupSessionMetadata(sessionIDs)
	if err != nil {
		return err
	}

	type projectAcc struct {
		entry          ProjectLogEntry
		sessionSet     map[string]struct{}
		modelSet       map[string]struct{}
		tokPerSecTotal float64
		durationCount  int
	}
	accs := make(map[string]*projectAcc)
	order := make([]string, 0)

	for _, r := range filtered {
		dir := dirs[r.SessionID]
		acc, ok := accs[dir]
		if !ok {
			acc = &projectAcc{
				entry:      ProjectLogEntry{Directory: dir},
				sessionSet: make(map[string]struct{}),
				modelSet:   make(map[string]struct{}),
			}
			accs[dir] = acc
			order = append(order, dir)
		}
		acc.sessionSet[r.SessionID] = struct{}{}
		acc.entry.Requests++
		acc.entry.InputTokens += r.InputTokens
		acc.entry.OutputTokens += r.OutputTokens
		acc.entry.CacheReadTokens += r.CacheReadTokens
		acc.entry.CacheWriteTokens += r.CacheWriteTokens
		acc.entry.TotalTokens += r.InputTokens + r.OutputTokens
		acc.entry.TotalDurationMs += r.DurationMs
		acc.entry.Cost += r.Cost
		acc.entry.CalcCost += r.CalcCost
		if r.DurationMs > 0 {
			acc.tokPerSecTotal += r.TokensPerSecond
			acc.durationCount++
		}
		if r.TimeCreated > acc.entry.LastRequestTime {
			acc.entry.LastRequestTime = r.TimeCreated
		}
		if r.Model != "" {
			acc.modelSet[r.Model] = struct{}{}
		}
		if r.StopReason == "error" {
			acc.entry.ErrorCount++
		}
	}

	entries := make([]ProjectLogEntry, 0, len(accs))
	for _, dir := range order {
		acc := accs[dir]
		if acc.durationCount > 0 {
			acc.entry.AvgTokensPerSec = acc.tokPerSecTotal / float64(acc.durationCount)
		}
		acc.entry.Sessions = len(acc.sessionSet)
		acc.entry.Models = sortedKeys(acc.modelSet)
		entries = append(entries, acc.entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastRequestTime > entries[j].LastRequestTime
	})

	dashboard.TotalProjects = len(entries)

	offset := projectOffset
	if offset < 0 {
		offset = 0
	}
	if offset > len(entries) {
		offset = len(entries)
	}
	paged := entries[offset:]
	if projectLimit > 0 && len(paged) > projectLimit {
		paged = paged[:projectLimit]
	}
	dashboard.Projects = paged
	return nil
}

// lookupSessionMetadata returns title and directory maps keyed by session id.
// Missing sessions simply have empty strings.
func (d *DB) lookupSessionMetadata(ids []string) (titles, dirs map[string]string, err error) {
	titles = make(map[string]string, len(ids))
	dirs = make(map[string]string, len(ids))
	if len(ids) == 0 {
		return titles, dirs, nil
	}
	// Build a parameterised IN clause; session counts in a filtered window
	// are typically small, so a single query is fine.
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := "SELECT id, title, directory FROM session WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, dir string
		if err := rows.Scan(&id, &title, &dir); err != nil {
			log.WithError(err).Warn("failed to scan session metadata row")
			continue
		}
		titles[id] = title
		dirs[id] = dir
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return titles, dirs, nil
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
//
// session_count excludes subagent sessions (titles like "... (xxx subagent)")
// so the figure matches the sessions list, which also hides them. Message,
// token and cost totals intentionally still include subagent activity
// because those represent real spend against the project.
func (d *DB) GetProjects() ([]ProjectStats, error) {
	rows, err := d.db.Query(`
		SELECT
			s.directory,
			SUM(CASE WHEN s.title NOT LIKE '%(% subagent)' THEN 1 ELSE 0 END) AS session_count,
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
// optionally filtered by a time window (since), model (modelFilter = "provider/model" or "model"),
// and directory prefix (dir; see directoryWhere).
func (d *DB) GetDailyActivity(since int64, modelFilter, dir string) ([]DailyActivity, error) {
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
		rows, err := d.db.Query(query, args...)
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
	query2 := `
		SELECT
			date(m.time_created / 1000, 'unixepoch', 'localtime') as day,
			m.data
		FROM message m
		JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND m.time_created >= ?
		  AND s.title NOT LIKE '%(% subagent)'`
	args2 := []interface{}{cutoff}
	if dirFrag != "" {
		query2 += "\n		  AND " + dirFrag
		args2 = append(args2, dirArgs...)
	}
	query2 += `
		ORDER BY day
	`
	rows2, err := d.db.Query(query2, args2...)
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

// GetModelUsage returns usage stats per model, optionally filtered by time
// window (since) and directory prefix (dir; see directoryWhere).
func (d *DB) GetModelUsage(since int64, dir string) ([]ModelUsage, error) {
	dirFrag, dirArgs := directoryWhere(dir)
	query := `SELECT m.data FROM message m`
	if dirFrag != "" {
		// Only join when scoping; preserves the existing query plan
		// for the unfiltered case.
		query += `
		JOIN session s ON s.id = m.session_id`
	}
	query += `
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND (? <= 0 OR m.time_created >= ?)`
	args := []interface{}{since, since}
	if dirFrag != "" {
		query += "\n		  AND " + dirFrag
		args = append(args, dirArgs...)
	}
	rows, err := d.db.Query(query, args...)
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

// RecentModel is one entry in the recent-models signal used by the composer
// model picker. `LastUsed` is a Unix-millis timestamp of the newest message
// that used this model.
type RecentModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	LastUsed int64  `json:"lastUsed"`
}

// GetRecentModels returns the distinct `provider/model` pairs most recently
// used across the N latest sessions, newest-message-first. This is a cheap
// alternative to the full usage aggregation (`GetModelUsage`): it limits work
// to recent sessions (index-backed on `time_updated`) and uses the compound
// session/time_created index when joining to messages.
//
// `sessionLimit` caps how many recent sessions to sample (typical: 50);
// `maxResults` caps the final list size (typical: 5–10).
func (d *DB) GetRecentModels(sessionLimit, maxResults int) ([]RecentModel, error) {
	if sessionLimit <= 0 {
		sessionLimit = 50
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	// Per-model: max timestamp of an assistant message, scoped to the N most
	// recently-updated sessions. Rely on SQL for the grouping/sort — way
	// cheaper than decoding N JSON blobs in Go just to pluck out a string.
	rows, err := d.db.Query(`
		SELECT
			COALESCE(
				NULLIF(json_extract(m.data, '$.providerID'), ''),
				NULLIF(json_extract(m.data, '$.model.providerID'), ''),
				''
			) AS provider,
			COALESCE(
				NULLIF(json_extract(m.data, '$.modelID'), ''),
				NULLIF(json_extract(m.data, '$.model.modelID'), ''),
				''
			) AS model,
			MAX(m.time_created) AS last_used
		FROM session s
		JOIN message m ON m.session_id = s.id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND s.id IN (SELECT id FROM session ORDER BY time_updated DESC LIMIT ?)
		GROUP BY provider, model
		HAVING model != ''
		ORDER BY last_used DESC
		LIMIT ?
	`, sessionLimit, maxResults)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]RecentModel, 0, maxResults)
	for rows.Next() {
		var rm RecentModel
		if err := rows.Scan(&rm.Provider, &rm.Model, &rm.LastUsed); err != nil {
			log.WithError(err).Warn("failed to scan recent model row")
			continue
		}
		result = append(result, rm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetHourlyTokensByModel returns token counts per calendar hour, broken down by provider/model.
// windowDays controls how many days back to look (default 7); modelFilter optionally restricts
// to one model key ("provider/model"); dir optionally scopes to a project subtree (see directoryWhere).
func (d *DB) GetHourlyTokensByModel(windowDays int, since int64, modelFilter, dir string) ([]HourlyTokensByModel, error) {
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
	rows, err := d.db.Query(query, args...)
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
func (d *DB) GetHourlyActivity(since int64, dir string) ([]HourlyActivity, error) {
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
	rows, err := d.db.Query(query, args...)
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
