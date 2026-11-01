package db

import (
	"context"
	"sort"
	"time"

	"github.com/ocman/ocman/internal/analyticsdata"
)

// The pre-summary dashboard implementation, kept only as a test oracle: it
// loads every request row and aggregates in Go. Summary-backed results must
// match it on the same fixture.

func (d *DB) legacyMetricsDashboard(ctx context.Context, opts MetricsDashboardOptions) (*MetricsDashboard, error) {
	filtered, agentSet, modelSet, err := d.scanHistoryRequests(ctx, opts)
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
	dashboard.Requests = paginateRequests(filtered, opts.RequestLimit, opts.RequestOffset)
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
