package db

import (
	"sort"
	"time"
)

// requestRow attaches computed fields without changing the request-log API.
type requestRow struct {
	RequestLogEntry
	estimatedCostByType CostByType
	completed           bool
	isError             bool
	modelStarted        int64
	agentDurationMs     int64
	toolDurationMs      int64
	unknownDurationMs   int64
}

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
	validThroughputCount := 0
	durations := make([]int64, 0, len(filtered))
	agents := make(map[string]*AgentMetrics)

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
			b.durations = append(b.durations, entry.DurationMs)
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
	dashboard.Summary.P50DurationMs = percentile(durations, 50)
	dashboard.Summary.P95DurationMs = percentile(durations, 95)
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

	dashboard.Series = buildDashboardSeries(buckets, bucketOrder, bucketFmt, days, since)
	dashboard.CostByModel = buildCostByModelSeries(buckets, dashboard.Series)
	dashboard.DailyEstimatedCostByModel = buildDailyEstimatedCostByModelSeries(buckets, dashboard.Series)
	dashboard.DailyEffectiveCostByModel = buildDailyEffectiveCostByModelSeries(buckets, dashboard.Series)
	return stopCounts
}
