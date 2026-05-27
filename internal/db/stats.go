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

// MetricsDashboardOptions groups the inputs to GetMetricsDashboard
// so callers don't have to remember the order of 11 positional
// parameters. Zero values for limit/offset fields are treated as
// "unbounded" / "no offset" — same as before the refactor.
//
// Field semantics:
//
//   - AgentFilter / ModelFilter: when non-empty, only rows whose
//     agent / "provider/model" match are kept in the aggregation.
//     Available* slices in the response always reflect the
//     pre-filter set so the dropdowns stay populated.
//   - Since: lower bound on `time_created` in milliseconds; <=0 = all time.
//   - Days: window length in days (0 = all time); drives bucket
//     granularity (hourly when 0 < Days <= 7, daily otherwise).
//   - RequestLimit / RequestOffset: pagination of the request log
//     (Requests slice). The window picks the most recent
//     RequestLimit entries.
//   - SessionLimit / SessionOffset: pagination of the per-session
//     aggregation (Sessions slice). Entries are sorted by total
//     Cost descending so the dashboard surfaces the most expensive
//     sessions first; ties break by most-recent activity.
//   - ProjectLimit / ProjectOffset: pagination of the per-project
//     aggregation (Projects slice). Same Cost-descending sort as
//     Sessions.
//   - Pricing: CostCalculator for computing CalcCost fields. Nil
//     leaves those fields zero (subscription-plan sessions surface
//     a zero CalcCost when pricing isn't configured).
//   - Dir: when non-empty, scopes the query to sessions whose
//     directory equals Dir or starts with Dir+"/". See directoryWhere.
type MetricsDashboardOptions struct {
	AgentFilter   string
	ModelFilter   string
	Since         int64
	Days          int
	RequestLimit  int
	RequestOffset int
	SessionLimit  int
	SessionOffset int
	ProjectLimit  int
	ProjectOffset int
	Pricing       CostCalculator
	Dir           string
}

// GetMetricsDashboard returns filtered request-level analytics for
// the dashboard. See [MetricsDashboardOptions] for field semantics.
//
// Returns a fully-populated *MetricsDashboard even on an empty result
// set — Series is zero-filled to opts.Days buckets so the frontend
// chart library can render a "no data" placeholder without a nil
// guard.
func (d *DB) GetMetricsDashboard(opts MetricsDashboardOptions) (*MetricsDashboard, error) {
	filtered, agentSet, modelSet, err := d.scanDashboardRows(opts)
	if err != nil {
		return nil, err
	}

	dashboard := &MetricsDashboard{
		AvailableAgents: sortedKeys(agentSet),
		AvailableModels: sortedKeys(modelSet),
	}

	stopCounts := d.aggregateSummaryAndBuckets(dashboard, filtered, opts.Days, opts.Since)

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
	dashboard.Requests = paginateRequests(filtered, opts.RequestLimit, opts.RequestOffset)

	if err := d.populateSessionLog(dashboard, filtered, opts.SessionLimit, opts.SessionOffset); err != nil {
		return nil, err
	}
	if err := d.populateProjectLog(dashboard, filtered, opts.ProjectLimit, opts.ProjectOffset); err != nil {
		return nil, err
	}
	return dashboard, nil
}

// scanDashboardRows queries assistant messages within the given options window
// and returns the filtered request rows plus the pre-filter agent/model sets
// (used to keep dropdowns populated even when a filter is active).
func (d *DB) scanDashboardRows(opts MetricsDashboardOptions) (filtered []requestRow, agentSet, modelSet map[string]struct{}, err error) {
	dirFrag, dirArgs := directoryWhere(opts.Dir)
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
	args := []interface{}{opts.Since, opts.Since}
	if dirFrag != "" {
		query += "\n		  AND " + dirFrag
		args = append(args, dirArgs...)
	}
	query += `
		ORDER BY m.time_created ASC
	`
	rows, qErr := d.db.Query(query, args...)
	if qErr != nil {
		return nil, nil, nil, qErr
	}
	defer rows.Close()

	agentSet = make(map[string]struct{})
	modelSet = make(map[string]struct{})

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

		if opts.AgentFilter != "" && agent != opts.AgentFilter {
			continue
		}
		if opts.ModelFilter != "" && modelKey != opts.ModelFilter {
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
		if opts.Pricing != nil {
			entry.CalcCost = opts.Pricing.CalcCost(modelKey, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, entry.CacheWriteTokens)
		}

		filtered = append(filtered, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return filtered, agentSet, modelSet, nil
}

// bucketAcc accumulates per-time-bucket metrics while building the series.
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
	// costByModel sums the platform-reported Cost in this bucket
	// partitioned by model key ("provider/model" or "" when missing).
	// Used by buildDashboardSeries to assemble the cost-by-model
	// cumulative series.
	costByModel   map[string]float64
	durationCount int
	count         int
}

// costByModelTopN is the maximum number of distinct models surfaced in
// the cost-by-model stacked chart. Remaining models are folded into an
// "Other" bucket so the legend stays readable.
const costByModelTopN = 6

// costByModelOtherKey is the synthetic model key used for the "Other"
// rollup bucket. Empty model strings (rows where the model couldn't be
// resolved) are also mapped onto this key.
const costByModelOtherKey = "Other"

// aggregateSummaryAndBuckets populates dashboard.Summary and dashboard.Series
// from the filtered rows and returns the stop-reason counts for later use.
func (d *DB) aggregateSummaryAndBuckets(dashboard *MetricsDashboard, filtered []requestRow, days int, since int64) map[string]int {
	hourly := days > 0 && days <= 7
	bucketFmt := "2006-01-02"
	if hourly {
		bucketFmt = "2006-01-02 15"
	}

	bucketOrder := make([]string, 0)
	buckets := make(map[string]*bucketAcc)
	stopCounts := make(map[string]int)
	validDurationCount := 0

	for _, entry := range filtered {
		dashboard.Summary.Requests++
		dashboard.Summary.TotalTokens += entry.InputTokens + entry.OutputTokens
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
			b = &bucketAcc{label: label, costByModel: make(map[string]float64)}
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
		b.costByModel[entry.Model] += entry.Cost
		b.count++
	}

	if validDurationCount > 0 {
		dashboard.Summary.AvgDurationMs /= float64(validDurationCount)
		dashboard.Summary.AvgTokensPerSec /= float64(validDurationCount)
	}
	if tc := dashboard.Summary.CacheReadTokens + dashboard.Summary.CacheWriteTokens; tc > 0 {
		dashboard.Summary.CacheHitRate = float64(dashboard.Summary.CacheReadTokens) / float64(tc)
	}

	dashboard.Series = buildDashboardSeries(buckets, bucketOrder, bucketFmt, days, since)
	dashboard.CostByModel = buildCostByModelSeries(buckets, dashboard.Series)
	return stopCounts
}

// buildDashboardSeries assembles the time-series with gap-filling. When days > 0
// every bucket in the window is emitted (with zeros for empty intervals) so the
// chart library can render a "no data" placeholder. All-time mode emits only
// the buckets that have data.
func buildDashboardSeries(buckets map[string]*bucketAcc, bucketOrder []string, bucketFmt string, days int, since int64) []MetricsPoint {
	var seriesLabels []string
	if days > 0 {
		start := time.UnixMilli(since).Local()
		now := time.Now().Local()
		hourly := days <= 7
		if hourly {
			cur := start.Truncate(time.Hour)
			for !cur.After(now) {
				seriesLabels = append(seriesLabels, cur.Format(bucketFmt))
				cur = cur.Add(time.Hour)
			}
		} else {
			cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
			for !cur.After(now) {
				seriesLabels = append(seriesLabels, cur.Format(bucketFmt))
				cur = cur.AddDate(0, 0, 1)
			}
		}
	} else {
		seriesLabels = bucketOrder
	}

	series := make([]MetricsPoint, 0, len(seriesLabels))
	cumCost, cumCalcCost := 0.0, 0.0
	for _, label := range seriesLabels {
		b := buckets[label] // nil for empty buckets
		var pt MetricsPoint
		pt.Label = label
		if b != nil {
			cumCost += b.totalCost
			cumCalcCost += b.totalCalcCost
			n := b.count
			if n < 1 {
				n = 1
			}
			avgDur := 0.0
			if b.durationCount > 0 {
				avgDur = b.totalDurationMs / float64(b.durationCount)
			}
			pt = MetricsPoint{
				Label:              label,
				AvgOutputTokensSec: b.totalOutputTokSec / float64(n),
				CumulativeCost:     cumCost,
				CumulativeCalcCost: cumCalcCost,
				InputTokens:        b.inputTokens,
				CacheReadTokens:    b.cacheReadTokens,
				OutputTokens:       b.outputTokens,
				AvgDurationMs:      avgDur,
				AvgCacheEfficiency: b.totalCacheEff / float64(n),
				Count:              b.count,
			}
		} else {
			pt = MetricsPoint{Label: label, CumulativeCost: cumCost, CumulativeCalcCost: cumCalcCost}
		}
		series = append(series, pt)
	}
	return series
}

// buildCostByModelSeries derives the per-model cumulative cost series
// used by the stacked cost chart. Models are ranked by total
// (whole-window) platform-reported cost; the top costByModelTopN are
// kept individually and the remainder folded into a single "Other"
// bucket. Empty model keys (rows without a resolved model) are also
// rolled into "Other" rather than rendering an unlabelled stack.
//
// The returned MetricsCostByModel.Series mirrors mainSeries one bucket
// for one bucket — including the gap-filled empty buckets — so the
// frontend can render it with the same x-axis labels as
// MetricsDashboard.Series.
func buildCostByModelSeries(buckets map[string]*bucketAcc, mainSeries []MetricsPoint) MetricsCostByModel {
	totals := make(map[string]float64)
	for _, b := range buckets {
		for model, cost := range b.costByModel {
			key := model
			if key == "" {
				key = costByModelOtherKey
			}
			totals[key] += cost
		}
	}
	if len(totals) == 0 {
		return MetricsCostByModel{Models: []string{}, Series: emptyModelCostSeries(mainSeries)}
	}

	// Stable ranking: cost desc, then name asc.
	type modelTotal struct {
		name string
		cost float64
	}
	ranked := make([]modelTotal, 0, len(totals))
	for name, cost := range totals {
		ranked = append(ranked, modelTotal{name: name, cost: cost})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].cost != ranked[j].cost {
			return ranked[i].cost > ranked[j].cost
		}
		return ranked[i].name < ranked[j].name
	})

	// Build the ordered model list and a key→column index map. The
	// "Other" bucket (if present) always sits at the tail.
	topModels := make([]string, 0, costByModelTopN)
	hasOther := false
	for _, mt := range ranked {
		if mt.name == costByModelOtherKey {
			// "Other" emerging organically (e.g. from empty model
			// rows) defers to the tail position even if its total
			// happens to rank inside the top-N window.
			hasOther = true
			continue
		}
		if len(topModels) < costByModelTopN {
			topModels = append(topModels, mt.name)
			continue
		}
		hasOther = true
	}
	// Always allocate a real slice (never nil) so JSON serialization
	// emits `[]` instead of `null` — the frontend's chart code relies
	// on `models.length` being defined.
	models := make([]string, 0, len(topModels)+1)
	models = append(models, topModels...)
	if hasOther {
		models = append(models, costByModelOtherKey)
	}

	colIdx := make(map[string]int, len(models))
	for i, m := range models {
		colIdx[m] = i
	}
	otherCol, hasOtherCol := colIdx[costByModelOtherKey]

	cum := make([]float64, len(models))
	out := MetricsCostByModel{
		Models: models,
		Series: make([]ModelCostPoint, 0, len(mainSeries)),
	}
	for _, pt := range mainSeries {
		if b, ok := buckets[pt.Label]; ok {
			for model, cost := range b.costByModel {
				key := model
				if key == "" {
					key = costByModelOtherKey
				}
				if idx, present := colIdx[key]; present {
					cum[idx] += cost
				} else if hasOtherCol {
					// Model didn't make the top-N; fold into "Other".
					cum[otherCol] += cost
				}
			}
		}
		costs := make([]float64, len(cum))
		copy(costs, cum)
		out.Series = append(out.Series, ModelCostPoint{Label: pt.Label, Costs: costs})
	}
	return out
}

// emptyModelCostSeries returns a CostByModel payload with no model
// columns but a label-aligned (all-zero-width) bucket list. Lets the
// frontend treat the field as always-present.
func emptyModelCostSeries(mainSeries []MetricsPoint) []ModelCostPoint {
	out := make([]ModelCostPoint, len(mainSeries))
	for i, pt := range mainSeries {
		out[i] = ModelCostPoint{Label: pt.Label, Costs: []float64{}}
	}
	return out
}

// paginateRequests returns the request log page for the given offset/limit,
// ordered most-recent-first.
func paginateRequests(filtered []requestRow, limit, offset int) []RequestLogEntry {
	if offset < 0 {
		offset = 0
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := len(filtered) - offset
	page := filtered[:end]
	if limit > 0 && len(page) > limit {
		page = page[len(page)-limit:]
	}
	out := make([]RequestLogEntry, len(page))
	for i, j := 0, len(page)-1; j >= 0; i, j = i+1, j-1 {
		out[i] = page[j].RequestLogEntry
	}
	return out
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

	// Sort by total cost descending so the dashboard surfaces the
	// most expensive sessions first — that's what users actually
	// want to scan when they open the metrics view. Ties (including
	// zero-cost subscription-plan sessions) break by most-recent
	// activity so the order is deterministic and intuitive.
	sort.Slice(entries, func(i, j int) bool {
		ci, cj := entries[i].Cost, entries[j].Cost
		if ci != cj {
			return ci > cj
		}
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

	// Sort by total cost descending so the most expensive projects
	// surface first. Ties break by most-recent activity for
	// determinism. Mirrors the per-session sort above.
	sort.Slice(entries, func(i, j int) bool {
		ci, cj := entries[i].Cost, entries[j].Cost
		if ci != cj {
			return ci > cj
		}
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
	rows3, err := d.db.Query(query3, args3...)
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
			if md.Tokens.Cache != nil {
				mu.CacheRead += md.Tokens.Cache.Read
				mu.CacheWrite += md.Tokens.Cache.Write
			}
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

// GetNewAssistantMessages returns assistant messages created after `since`
// (exclusive), ordered by time_created ASC. It also returns the new
// high-water mark (the max time_created seen, or `since` if no rows).
//
// This is the data source for the LLM metrics scanner: it runs every
// collection interval, feeds each row into OTel counters/histograms,
// and advances the high-water mark so the next call only sees new data.
func (d *DB) GetNewAssistantMessages(since int64) ([]LLMMessageRow, int64, error) {
	rows, err := d.db.Query(`
		SELECT m.time_created, m.data
		FROM message m
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND m.time_created > ?
		ORDER BY m.time_created ASC
	`, since)
	if err != nil {
		return nil, since, err
	}
	defer rows.Close()

	var result []LLMMessageRow
	hwm := since
	for rows.Next() {
		var tc int64
		var raw string
		if err := rows.Scan(&tc, &raw); err != nil {
			log.WithError(err).Warn("failed to scan LLM message row")
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			log.WithError(err).Warn("failed to unmarshal LLM message data")
			continue
		}

		provider, model := extractModelProvider(md)
		modelKey := model
		if provider != "" && model != "" {
			modelKey = provider + "/" + model
		}

		r := LLMMessageRow{
			TimeCreated: tc,
			Model:       modelKey,
			Cost:        md.Cost,
		}
		if md.Tokens != nil {
			r.InputTokens = md.Tokens.Input
			r.OutputTokens = md.Tokens.Output
			if md.Tokens.Cache != nil {
				r.CacheReadTokens = md.Tokens.Cache.Read
				r.CacheWriteTokens = md.Tokens.Cache.Write
			}
		}
		r.StopReason = strings.TrimSpace(md.Finish)
		if r.StopReason == "" {
			r.StopReason = "none"
		}
		if md.Time != nil && md.Time.Completed > md.Time.Created {
			r.DurationMs = md.Time.Completed - md.Time.Created
		}

		result = append(result, r)
		if tc > hwm {
			hwm = tc
		}
	}
	if err := rows.Err(); err != nil {
		return nil, since, err
	}
	return result, hwm, nil
}

// GetMaxMessageTime returns the maximum time_created across all messages,
// or 0 if the table is empty. Used to initialise the LLM metrics scanner's
// high-water mark so it only emits metrics for messages arriving after
// ocman starts.
func (d *DB) GetMaxMessageTime() (int64, error) {
	var maxTime int64
	err := d.db.QueryRow(`SELECT COALESCE(MAX(time_created), 0) FROM message`).Scan(&maxTime)
	return maxTime, err
}
