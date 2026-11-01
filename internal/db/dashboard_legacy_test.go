package db

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/analyticsdata"
)

// The pre-summary dashboard implementation, kept only as a test oracle: it
// loads every request row and aggregates in Go. Summary-backed results must
// match it on the same fixture.

func (d *DB) legacyMetricsDashboard(ctx context.Context, opts MetricsDashboardOptions) (*MetricsDashboard, error) {
	filtered, agentSet, modelSet, err := d.legacyScanRequests(ctx, opts)
	if err != nil {
		return nil, err
	}
	dashboard := &MetricsDashboard{AvailableAgents: sortedKeys(agentSet), AvailableModels: sortedKeys(modelSet)}
	stopCounts := legacyAggregateSummaryAndBuckets(dashboard, filtered, opts.Days, opts.Since)
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
	dashboard.Requests = legacyPaginateRequests(filtered, opts.RequestLimit, opts.RequestOffset)
	legacyPopulateSessionLog(dashboard, filtered, opts.SessionLimit, opts.SessionOffset)
	legacyPopulateProjectLog(dashboard, filtered, opts.ProjectLimit, opts.ProjectOffset)
	return dashboard, nil
}

// aggregateSummaryAndBuckets populates dashboard.Summary and dashboard.Series
// from the filtered rows and returns the stop-reason counts for later use.
func legacyAggregateSummaryAndBuckets(dashboard *MetricsDashboard, filtered []requestRow, days int, since int64) map[string]int {
	hourly := days > 0 && days <= 7
	bucketFmt := "2006-01-02"
	if hourly {
		bucketFmt = "2006-01-02 15"
	}

	bucketOrder := make([]string, 0)
	buckets := make(map[string]*bucketAcc)
	stopCounts := make(map[string]int)
	validDurationCount := 0
	validThroughputCount := 0
	durations := make([]int64, 0, len(filtered))
	agents := make(map[string]*AgentMetrics)
	durationsByLabel := map[string][]int64{}

	for _, entry := range filtered {
		dashboard.Summary.Requests++
		dashboard.Summary.TotalTokens += entry.InputTokens + entry.OutputTokens
		dashboard.Summary.InputTokens += entry.InputTokens
		dashboard.Summary.OutputTokens += entry.OutputTokens
		dashboard.Summary.CacheReadTokens += entry.CacheReadTokens
		dashboard.Summary.CacheWriteTokens += entry.CacheWriteTokens
		dashboard.Summary.TotalCost += entry.Cost
		dashboard.Summary.TotalCalcCost += entry.CalcCost
		dashboard.Summary.EstimatedCostByType.Input += entry.estimatedCostByType.Input
		dashboard.Summary.EstimatedCostByType.Output += entry.estimatedCostByType.Output
		dashboard.Summary.EstimatedCostByType.CacheRead += entry.estimatedCostByType.CacheRead
		dashboard.Summary.EstimatedCostByType.CacheWrite += entry.estimatedCostByType.CacheWrite
		dashboard.Summary.TotalEffectiveCost += entry.EffectiveCost
		if entry.completed {
			dashboard.Summary.CompletedRequests++
		}
		if entry.isError {
			dashboard.Summary.ErrorRequests++
		} else if entry.completed {
			dashboard.Summary.SuccessfulRequests++
		}
		if entry.DurationMs > 0 {
			durations = append(durations, entry.DurationMs)
			dashboard.Summary.AvgDurationMs += float64(entry.DurationMs)
			dashboard.Summary.TotalDurationMs += entry.DurationMs
			validDurationCount++
		}
		if entry.TokensPerSecond > 0 {
			dashboard.Summary.AvgTokensPerSec += entry.TokensPerSecond
			validThroughputCount++
		}
		stopCounts[entry.StopReason]++

		label := time.UnixMilli(entry.TimeCreated).Local().Format(bucketFmt)
		b, ok := buckets[label]
		if !ok {
			b = &bucketAcc{
				label:                label,
				costByModel:          make(map[string]float64),
				estimatedCostByModel: make(map[string]float64),
			}
			buckets[label] = b
			bucketOrder = append(bucketOrder, label)
		}
		b.inputTokens += entry.InputTokens
		b.cacheReadTokens += entry.CacheReadTokens
		b.cacheWriteTokens += entry.CacheWriteTokens
		b.outputTokens += entry.OutputTokens
		b.totalOutputTokSec += entry.TokensPerSecond
		if entry.TokensPerSecond > 0 {
			b.throughputCount++
		}
		if entry.DurationMs > 0 {
			b.totalDurationMs += float64(entry.DurationMs)
			durationsByLabel[label] = append(durationsByLabel[label], entry.DurationMs)
			b.durationCount++
		}
		if entry.completed {
			b.completedRequests++
		}
		if entry.isError {
			b.errorRequests++
		} else if entry.completed {
			b.successfulRequests++
		}
		cacheEff := 0.0
		if tc := entry.CacheReadTokens + entry.CacheWriteTokens; tc > 0 {
			cacheEff = float64(entry.CacheReadTokens) / float64(tc)
		}
		b.totalCacheEff += cacheEff
		b.totalCost += entry.Cost
		b.totalCalcCost += entry.CalcCost
		b.totalEffectiveCost += entry.EffectiveCost
		b.costByModel[entry.Model] += entry.EffectiveCost
		b.estimatedCostByModel[entry.Model] += entry.CalcCost
		b.count++

		agent := agents[entry.Agent]
		if agent == nil {
			agent = &AgentMetrics{Agent: entry.Agent}
			agents[entry.Agent] = agent
		}
		agent.Requests++
		agent.InputTokens += entry.InputTokens
		agent.OutputTokens += entry.OutputTokens
		agent.TotalTokens += entry.InputTokens + entry.OutputTokens
		agent.TotalDurationMs += entry.DurationMs
		agent.AgentDurationMs += entry.agentDurationMs
		agent.ToolDurationMs += entry.toolDurationMs
		agent.UnknownDurationMs += entry.unknownDurationMs
		agent.EffectiveCost += entry.EffectiveCost
		if entry.isError {
			agent.ErrorRequests++
		} else if entry.completed {
			agent.SuccessfulRequests++
		}
	}

	if validDurationCount > 0 {
		dashboard.Summary.AvgDurationMs /= float64(validDurationCount)
	}
	if validThroughputCount > 0 {
		dashboard.Summary.AvgTokensPerSec /= float64(validThroughputCount)
	}
	if tc := dashboard.Summary.CacheReadTokens + dashboard.Summary.CacheWriteTokens; tc > 0 {
		dashboard.Summary.CacheHitRate = float64(dashboard.Summary.CacheReadTokens) / float64(tc)
	}
	dashboard.Summary.P50DurationMs = float64(analyticsdata.Percentile(durations, 50))
	dashboard.Summary.P95DurationMs = float64(analyticsdata.Percentile(durations, 95))
	if outcomes := dashboard.Summary.SuccessfulRequests + dashboard.Summary.ErrorRequests; outcomes > 0 {
		dashboard.Summary.ErrorRate = float64(dashboard.Summary.ErrorRequests) / float64(outcomes)
	}
	if dashboard.Summary.SuccessfulRequests > 0 {
		dashboard.Summary.CostPerSuccessfulRequest = dashboard.Summary.TotalEffectiveCost / float64(dashboard.Summary.SuccessfulRequests)
	}
	dashboard.Agents = make([]AgentMetrics, 0, len(agents))
	for _, agent := range agents {
		if outcomes := agent.SuccessfulRequests + agent.ErrorRequests; outcomes > 0 {
			agent.ErrorRate = float64(agent.ErrorRequests) / float64(outcomes)
		}
		dashboard.Agents = append(dashboard.Agents, *agent)
	}
	sort.Slice(dashboard.Agents, func(i, j int) bool {
		if dashboard.Agents[i].EffectiveCost != dashboard.Agents[j].EffectiveCost {
			return dashboard.Agents[i].EffectiveCost > dashboard.Agents[j].EffectiveCost
		}
		return dashboard.Agents[i].Agent < dashboard.Agents[j].Agent
	})

	for label, b := range buckets {
		b.p50 = analyticsdata.Percentile(durationsByLabel[label], 50)
		b.p95 = analyticsdata.Percentile(durationsByLabel[label], 95)
	}
	dashboard.Series = buildDashboardSeries(buckets, bucketOrder, bucketFmt, days, since)
	effective := func(b *bucketAcc) map[string]float64 { return b.costByModel }
	estimated := func(b *bucketAcc) map[string]float64 { return b.estimatedCostByModel }
	// CostByModel stays cumulative for existing clients; the daily series
	// group by local calendar day for the Stats chart.
	dashboard.CostByModel = buildModelCostSeries(buckets, dashboard.Series, effective, false)
	dashboard.DailyEstimatedCostByModel = buildModelCostSeries(buckets, dashboard.Series, estimated, true)
	dashboard.DailyEffectiveCostByModel = buildModelCostSeries(buckets, dashboard.Series, effective, true)
	return stopCounts
}

func (a *logAcc) legacyAdd(r requestRow) {
	a.requests++
	a.in += r.InputTokens
	a.out += r.OutputTokens
	a.cacheRead += r.CacheReadTokens
	a.cacheWrite += r.CacheWriteTokens
	a.duration += r.DurationMs
	a.cost += r.Cost
	a.calcCost += r.CalcCost
	a.effectiveCost += r.EffectiveCost
	if r.TokensPerSecond > 0 {
		a.tokPerSec += r.TokensPerSecond
		a.durationCount++
	}
	a.first = min(a.first, r.TimeCreated)
	a.last = max(a.last, r.TimeCreated)
	if r.Agent != "" {
		a.agents[r.Agent] = struct{}{}
	}
	if r.Model != "" {
		a.models[r.Model] = struct{}{}
	}
	a.sessions[r.SessionID] = struct{}{}
	if r.isError {
		a.errors++
	}
}

// groupRequests accumulates rows by key in first-seen order. Titles and
// directories come from the same retained snapshot as the request rows.
func legacyGroupRequests(rows []requestRow, key func(requestRow) string) ([]string, map[string]*logAcc) {
	var order []string
	accs := make(map[string]*logAcc)
	for _, r := range rows {
		k := key(r)
		acc, ok := accs[k]
		if !ok {
			acc = &logAcc{
				title: r.sessionTitle, directory: r.sessionDirectory,
				first: r.TimeCreated, last: r.TimeCreated,
				agents: map[string]struct{}{}, models: map[string]struct{}{}, sessions: map[string]struct{}{},
			}
			accs[k] = acc
			order = append(order, k)
		}
		acc.legacyAdd(r)
	}
	return order, accs
}

// populateSessionLog aggregates the already-filtered request rows by session id
// and applies pagination.
func legacyPopulateSessionLog(dashboard *MetricsDashboard, filtered []requestRow, sessionLimit, sessionOffset int) {
	order, accs := legacyGroupRequests(filtered, func(r requestRow) string { return r.SessionID })
	entries := make([]SessionLogEntry, 0, len(accs))
	for _, id := range order {
		a := accs[id]
		entries = append(entries, SessionLogEntry{
			ID: id, Title: a.title, Directory: a.directory,
			FirstRequestTime: a.first, LastRequestTime: a.last,
			Requests: a.requests, InputTokens: a.in, OutputTokens: a.out,
			CacheReadTokens: a.cacheRead, CacheWriteTokens: a.cacheWrite,
			TotalTokens: a.in + a.out, TotalDurationMs: a.duration, AvgTokensPerSec: a.avgTokensPerSec(),
			Cost: a.cost, CalcCost: a.calcCost, EffectiveCost: a.effectiveCost,
			Agents: sortedKeys(a.agents), Models: sortedKeys(a.models), ErrorCount: a.errors,
		})
	}

	// Sort by total cost descending so the dashboard surfaces the
	// most expensive sessions first — that's what users actually
	// want to scan when they open the metrics view.
	sort.Slice(entries, func(i, j int) bool {
		return costDescLess(entries[i].Cost, entries[j].Cost,
			entries[i].LastRequestTime, entries[j].LastRequestTime)
	})

	dashboard.TotalSessions = len(entries)
	dashboard.Sessions = paginateSlice(entries, sessionOffset, sessionLimit)
}

// populateProjectLog aggregates the already-filtered request rows by project
// directory and applies pagination using each row's retained session metadata.
func legacyPopulateProjectLog(dashboard *MetricsDashboard, filtered []requestRow, projectLimit, projectOffset int) {
	order, accs := legacyGroupRequests(filtered, func(r requestRow) string { return r.sessionDirectory })
	entries := make([]ProjectLogEntry, 0, len(accs))
	for _, dir := range order {
		a := accs[dir]
		entries = append(entries, ProjectLogEntry{
			Directory: dir, Sessions: len(a.sessions),
			Requests: a.requests, InputTokens: a.in, OutputTokens: a.out,
			CacheReadTokens: a.cacheRead, CacheWriteTokens: a.cacheWrite,
			TotalTokens: a.in + a.out, TotalDurationMs: a.duration, AvgTokensPerSec: a.avgTokensPerSec(),
			Cost: a.cost, CalcCost: a.calcCost, EffectiveCost: a.effectiveCost,
			Models: sortedKeys(a.models), ErrorCount: a.errors, LastRequestTime: a.last,
		})
	}

	// Sort by total cost descending so the most expensive projects
	// surface first. Mirrors the per-session sort above.
	sort.Slice(entries, func(i, j int) bool {
		return costDescLess(entries[i].Cost, entries[j].Cost,
			entries[i].LastRequestTime, entries[j].LastRequestTime)
	})

	dashboard.TotalProjects = len(entries)
	dashboard.Projects = paginateSlice(entries, projectOffset, projectLimit)
}

// requestRow attaches computed fields without changing the request-log API.
type requestRow struct {
	RequestLogEntry
	sessionTitle, sessionDirectory string
	estimatedCostByType            CostByType
	completed                      bool
	isError                        bool
	modelStarted                   int64
	agentDurationMs                int64
	toolDurationMs                 int64
	unknownDurationMs              int64
}

func (d *DB) legacyScanRequests(ctx context.Context, opts MetricsDashboardOptions) ([]requestRow, map[string]struct{}, map[string]struct{}, error) {
	query := `SELECT m.instance_id,s.title,s.directory,m.id,m.session_id,m.time_created,m.agent,m.provider,m.model,m.cost,
		m.input_tokens,m.output_tokens,m.cache_read,m.cache_write,m.started_at,m.duration_ms,
		m.agent_ms,m.tool_ms,m.unknown_ms,m.tokens_per_second,m.finish,m.is_error,m.is_completed
		FROM history_fact m JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
		WHERE m.role='assistant' AND m.valid=1`
	var args []any
	if opts.Since > 0 {
		query += ` AND m.time_created>=?`
		args = append(args, opts.Since)
	}
	if clause, values := directoryWhere(opts.Dir); clause != "" {
		query += ` AND ` + clause
		args = append(args, values...)
	}
	query += ` ORDER BY m.time_created ASC,m.id ASC,m.session_id ASC,m.instance_id ASC` // ties were unordered; pin them like the SQL page
	rows, err := d.reads().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	agents, models := map[string]struct{}{}, map[string]struct{}{}
	var result []requestRow
	for rows.Next() {
		var r requestRow
		var provider, model string
		var instance string
		if err := rows.Scan(&instance, &r.sessionTitle, &r.sessionDirectory, &r.ID, &r.SessionID, &r.TimeCreated, &r.Agent, &provider, &model, &r.Cost,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.modelStarted, &r.DurationMs,
			&r.agentDurationMs, &r.toolDurationMs, &r.unknownDurationMs, &r.TokensPerSecond, &r.StopReason, &r.isError, &r.completed); err != nil {
			return nil, nil, nil, err
		}
		r.Agent = strings.TrimSpace(r.Agent)
		r.Model = analyticsdata.ModelKey(provider, strings.TrimSpace(model))
		if r.Agent != "" {
			agents[r.Agent] = struct{}{}
		}
		if r.Model != "" {
			models[r.Model] = struct{}{}
		}
		if (opts.AgentFilter != "" && opts.AgentFilter != r.Agent) || (opts.ModelFilter != "" && opts.ModelFilter != r.Model) {
			continue
		}
		if opts.SessionIdentity != nil {
			id, err := opts.SessionIdentity(ctx, instance, r.SessionID)
			if err != nil {
				return nil, nil, nil, err
			}
			r.SessionID = id
		}
		if r.StopReason == "" {
			r.StopReason = "none"
		}
		legacyPriceRequest(&r, opts.Pricing)
		result = append(result, r)
	}
	return result, agents, models, rows.Err()
}

func legacyPriceRequest(entry *requestRow, pricing CostCalculator) {
	if pricing != nil {
		entry.CalcCost = pricing.CalcCost(entry.Model, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, entry.CacheWriteTokens)
		entry.estimatedCostByType = CostByType{
			Input: pricing.CalcCost(entry.Model, entry.InputTokens, 0, 0, 0), Output: pricing.CalcCost(entry.Model, 0, entry.OutputTokens, 0, 0),
			CacheRead: pricing.CalcCost(entry.Model, 0, 0, entry.CacheReadTokens, 0), CacheWrite: pricing.CalcCost(entry.Model, 0, 0, 0, entry.CacheWriteTokens),
		}
	}
	entry.EffectiveCost = effectiveCost(entry.Cost, entry.CalcCost)
}

// paginateRequests returns the request log page for the given offset/limit,
// ordered most-recent-first.
func legacyPaginateRequests(filtered []requestRow, limit, offset int) []RequestLogEntry {
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
