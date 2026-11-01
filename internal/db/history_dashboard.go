package db

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/analyticsdata"
)

const dashboardHour = int64(3600000)

// dashboardHourAggregates mirrors statev2's analytics_hour aggregates so exact
// boundary facts fold into the same shape as maintained hour rows.
const dashboardHourAggregates = `min(time_created),max(time_created),count(*),
	sum(input_tokens),sum(output_tokens),sum(cache_read),sum(cache_write),sum(cost),
	sum(is_completed),sum(is_error),sum(is_completed AND NOT is_error),
	sum(duration_ms),sum(duration_ms>0),sum(agent_ms),sum(tool_ms),sum(unknown_ms),
	sum(CASE WHEN tokens_per_second>0 THEN tokens_per_second ELSE 0 END),sum(tokens_per_second>0),
	sum(CASE WHEN cache_read+cache_write>0 THEN cache_read*1.0/(cache_read+cache_write) ELSE 0 END),
	sum(CASE WHEN cost>0 THEN cost ELSE 0 END),
	sum(CASE WHEN cost>0 THEN 0 ELSE input_tokens END),sum(CASE WHEN cost>0 THEN 0 ELSE output_tokens END),
	sum(CASE WHEN cost>0 THEN 0 ELSE cache_read END),sum(CASE WHEN cost>0 THEN 0 ELSE cache_write END)`

// dashboardGroup aggregates the valid assistant requests of one session,
// calendar label, agent, model and stop reason. Pricing and session identity
// are applied when assembling a response, so groups are safe to share.
type dashboardGroup struct {
	instance, session, title, directory  string
	label                                string
	rawAgent, provider, rawModel, finish string
	agent, model                         string
	first, last                          int64
	messages                             int
	in, out, read, write                 int64
	cost                                 float64
	completed, errors, successes         int
	duration                             int64
	timed                                int
	agentMs, toolMs, unknownMs           int64
	tps                                  float64
	throughput                           int
	cacheEfficiency                      float64
	pricedCost                           float64
	unpricedIn, unpricedOut              int64
	unpricedRead, unpricedWrite          int64
}

func (g *dashboardGroup) sums() []any {
	return []any{&g.first, &g.last, &g.messages, &g.in, &g.out, &g.read, &g.write, &g.cost,
		&g.completed, &g.errors, &g.successes, &g.duration, &g.timed, &g.agentMs, &g.toolMs, &g.unknownMs,
		&g.tps, &g.throughput, &g.cacheEfficiency, &g.pricedCost, &g.unpricedIn, &g.unpricedOut, &g.unpricedRead, &g.unpricedWrite}
}

func (g *dashboardGroup) matches(opts MetricsDashboardOptions) bool {
	return (opts.AgentFilter == "" || opts.AgentFilter == g.agent) && (opts.ModelFilter == "" || opts.ModelFilter == g.model)
}

// pricedGroup carries the pricing-derived costs of one group. CostCalculator
// implementations are linear in token counts per model, so pricing summed
// tokens equals summing per-request prices.
type pricedGroup struct {
	calc, effective float64
	byType          CostByType
}

func (g *dashboardGroup) price(pricing CostCalculator) pricedGroup {
	if pricing == nil {
		return pricedGroup{effective: g.pricedCost}
	}
	return pricedGroup{
		calc:      pricing.CalcCost(g.model, g.in, g.out, g.read, g.write),
		effective: g.pricedCost + pricing.CalcCost(g.model, g.unpricedIn, g.unpricedOut, g.unpricedRead, g.unpricedWrite),
		byType: CostByType{
			Input: pricing.CalcCost(g.model, g.in, 0, 0, 0), Output: pricing.CalcCost(g.model, 0, g.out, 0, 0),
			CacheRead: pricing.CalcCost(g.model, 0, 0, g.read, 0), CacheWrite: pricing.CalcCost(g.model, 0, 0, 0, g.write),
		},
	}
}

type labelRange struct {
	start, end int64
	label      string
}

type durationPercentiles struct{ p50, p95 int64 }

// dashboardWindow is every request group in one directory/time window plus
// the calendar ranges they cover. It is independent of agent/model filters.
type dashboardWindow struct {
	groups  []dashboardGroup
	ranges  []labelRange
	agents  map[string]struct{}
	models  map[string]struct{}
	layout  string
	sinceAt int64
}

func dashboardLayout(days int) string {
	if days > 0 && days <= 7 {
		return "2006-01-02 15"
	}
	return "2006-01-02"
}

func calendarLabel(at int64, layout string) string {
	return time.UnixMilli(at).Local().Format(layout)
}

// splitByLabel returns the calendar ranges of [start,end). A UTC hour may
// split a local hour or day (e.g. Asia/Kathmandu, or a DST transition).
func splitByLabel(start, end int64, layout string) []labelRange {
	var out []labelRange
	for start < end {
		label := calendarLabel(start, layout)
		if calendarLabel(end-1, layout) == label {
			return append(out, labelRange{start, end, label})
		}
		lo, hi := start, end-1
		for hi-lo > 1 {
			mid := lo + (hi-lo)/2
			if calendarLabel(mid, layout) == label {
				lo = mid
			} else {
				hi = mid
			}
		}
		out = append(out, labelRange{start, hi, label})
		start = hi
	}
	return out
}

// loadDashboardWindow reads full UTC hours from analytics_hour and folds only
// the boundary hours (the partial first hour and hours that split a calendar
// label) from exact facts, grouped in SQLite.
func (d *DB) loadDashboardWindow(ctx context.Context, opts MetricsDashboardOptions) (*dashboardWindow, error) {
	layout := dashboardLayout(opts.Days)
	since := max(0, opts.Since)
	floor := since - since%dashboardHour
	edge := floor
	if since%dashboardHour != 0 {
		if floor <= math.MaxInt64-dashboardHour {
			edge += dashboardHour
		} else {
			edge = math.MaxInt64
		}
	}
	dirClause, dirArgs := directoryWhere(opts.Dir)
	dirFilter := ""
	if dirClause != "" {
		dirFilter = ` AND ` + dirClause
	}
	query := `SELECT h.instance_id,h.session_id,s.title,s.directory,h.bucket,h.agent,h.provider,h.model,h.finish,
		h.first_at,h.last_at,h.messages,h.input_tokens,h.output_tokens,h.cache_read,h.cache_write,h.cost,
		h.completed,h.errors,h.successes,h.duration_ms,h.timed,h.agent_ms,h.tool_ms,h.unknown_ms,
		h.tokens_per_second,h.throughput,h.cache_efficiency,h.priced_cost,h.unpriced_input,h.unpriced_output,h.unpriced_read,h.unpriced_write
		FROM history_hour h JOIN session s ON s.instance_id=h.instance_id AND s.id=h.session_id
		WHERE h.role='assistant' AND h.valid=1 AND h.bucket>=?` + dirFilter
	rows, err := d.reads().QueryContext(ctx, query, append([]any{floor}, dirArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	w := &dashboardWindow{layout: layout, sinceAt: since, agents: map[string]struct{}{}, models: map[string]struct{}{}}
	hours := map[int64]bool{} // bucket -> clean
	for rows.Next() {
		var g dashboardGroup
		var bucket int64
		if err := rows.Scan(append([]any{&g.instance, &g.session, &g.title, &g.directory, &bucket, &g.rawAgent, &g.provider, &g.rawModel, &g.finish}, g.sums()...)...); err != nil {
			return nil, err
		}
		g.label = calendarLabel(bucket, layout)
		clean, seen := hours[bucket]
		if !seen {
			clean = bucket >= edge && g.label == calendarLabel(bucket+dashboardHour-1, layout)
			hours[bucket] = clean
		}
		if clean {
			w.add(g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	buckets := make([]int64, 0, len(hours))
	for bucket := range hours {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	var boundary []labelRange
	for _, bucket := range buckets {
		if hours[bucket] {
			w.addRange(labelRange{bucket, bucket + dashboardHour, calendarLabel(bucket, layout)})
			continue
		}
		for _, r := range splitByLabel(max(bucket, since), bucket+dashboardHour, layout) {
			boundary = append(boundary, r)
			w.addRange(r)
		}
	}
	if len(boundary) == 0 {
		return w, nil
	}
	raw, err := json.Marshal(rangesJSON(boundary))
	if err != nil {
		return nil, err
	}
	query = `SELECT instance_id,session_id,title,directory,k,agent,provider,model,finish,` + dashboardHourAggregates + `
		FROM (SELECT f.*,s.title,s.directory,json_extract(r.value,'$[2]') AS k
			FROM json_each(?) r CROSS JOIN history_fact f JOIN session s ON s.instance_id=f.instance_id AND s.id=f.session_id
			WHERE f.role='assistant' AND f.valid=1 AND f.time_created>=json_extract(r.value,'$[0]') AND f.time_created<json_extract(r.value,'$[1]')` + dirFilter + `)
		GROUP BY instance_id,session_id,k,agent,provider,model,finish`
	rows, err = d.reads().QueryContext(ctx, query, append([]any{string(raw)}, dirArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g dashboardGroup
		if err := rows.Scan(append([]any{&g.instance, &g.session, &g.title, &g.directory, &g.label, &g.rawAgent, &g.provider, &g.rawModel, &g.finish}, g.sums()...)...); err != nil {
			return nil, err
		}
		w.add(g)
	}
	return w, rows.Err()
}

func (w *dashboardWindow) add(g dashboardGroup) {
	g.agent = strings.TrimSpace(g.rawAgent)
	g.model = analyticsdata.ModelKey(g.provider, strings.TrimSpace(g.rawModel))
	if g.agent != "" {
		w.agents[g.agent] = struct{}{}
	}
	if g.model != "" {
		w.models[g.model] = struct{}{}
	}
	w.groups = append(w.groups, g)
}

// addRange appends r, merging it into the previous range when contiguous.
func (w *dashboardWindow) addRange(r labelRange) {
	if n := len(w.ranges); n > 0 && w.ranges[n-1].end == r.start && w.ranges[n-1].label == r.label {
		w.ranges[n-1].end = r.end
		return
	}
	w.ranges = append(w.ranges, r)
}

func rangesJSON(ranges []labelRange) [][3]any {
	out := make([][3]any, len(ranges))
	for i, r := range ranges {
		out[i] = [3]any{r.start, r.end, r.label}
	}
	return out
}

// filtered returns the groups matching the agent/model filters, ordered by
// first request so first-seen titles and log order match the request order.
func (w *dashboardWindow) filtered(opts MetricsDashboardOptions) []dashboardGroup {
	out := make([]dashboardGroup, 0, len(w.groups))
	for _, g := range w.groups {
		if g.matches(opts) {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].first < out[j].first })
	return out
}

// durationPercentiles ranks request durations inside SQLite, per calendar
// label and for the whole window, returning only the selected ranks.
func (d *DB) durationPercentiles(ctx context.Context, w *dashboardWindow, opts MetricsDashboardOptions, groups []dashboardGroup) (durationPercentiles, map[string]durationPercentiles, error) {
	var overall durationPercentiles
	byLabel := map[string]durationPercentiles{}
	if len(w.ranges) == 0 || len(groups) == 0 {
		return overall, byLabel, nil
	}
	rawRanges, err := json.Marshal(rangesJSON(w.ranges))
	if err != nil {
		return overall, byLabel, err
	}
	args := []any{string(rawRanges)}
	filter := ""
	if dirClause, dirArgs := directoryWhere(opts.Dir); dirClause != "" {
		filter += ` AND ` + dirClause
		args = append(args, dirArgs...)
	}
	if opts.AgentFilter != "" || opts.ModelFilter != "" {
		seen := map[[3]string]bool{}
		var combos [][3]string
		for _, g := range groups {
			key := [3]string{g.rawAgent, g.provider, g.rawModel}
			if !seen[key] {
				seen[key] = true
				combos = append(combos, key)
			}
		}
		rawCombos, err := json.Marshal(combos)
		if err != nil {
			return overall, byLabel, err
		}
		filter += ` AND EXISTS(SELECT 1 FROM json_each(?) c WHERE json_extract(c.value,'$[0]')=f.agent
			AND json_extract(c.value,'$[1]')=f.provider AND json_extract(c.value,'$[2]')=f.model)`
		args = append(args, string(rawCombos))
	}
	rows, err := d.reads().QueryContext(ctx, `WITH r AS (SELECT json_extract(value,'$[0]') lo,json_extract(value,'$[1]') hi,json_extract(value,'$[2]') k FROM json_each(?)),
		d AS (SELECT r.k k,f.duration_ms v FROM r CROSS JOIN history_fact f JOIN session s ON s.instance_id=f.instance_id AND s.id=f.session_id
			WHERE f.role='assistant' AND f.valid=1 AND f.duration_ms>0 AND f.time_created>=r.lo AND f.time_created<r.hi`+filter+`),
		o AS (SELECT k,v,row_number() OVER (PARTITION BY k ORDER BY v) rn,count(*) OVER (PARTITION BY k) n,
			row_number() OVER (ORDER BY v) arn,count(*) OVER () an FROM d)
		SELECT k,v,rn=(n*50+99)/100,rn=(n*95+99)/100,arn=(an*50+99)/100,arn=(an*95+99)/100 FROM o
		WHERE rn IN ((n*50+99)/100,(n*95+99)/100) OR arn IN ((an*50+99)/100,(an*95+99)/100)`, args...)
	if err != nil {
		return overall, byLabel, err
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		var value int64
		var p50, p95, all50, all95 bool
		if err := rows.Scan(&label, &value, &p50, &p95, &all50, &all95); err != nil {
			return overall, byLabel, err
		}
		p := byLabel[label]
		if p50 {
			p.p50 = value
		}
		if p95 {
			p.p95 = value
		}
		byLabel[label] = p
		if all50 {
			overall.p50 = value
		}
		if all95 {
			overall.p95 = value
		}
	}
	return overall, byLabel, rows.Err()
}

// aggregateGroups populates the summary, series, cost-by-model charts and
// agent breakdown from request groups, returning stop-reason counts.
func aggregateGroups(dashboard *MetricsDashboard, groups []dashboardGroup, overall durationPercentiles, byLabel map[string]durationPercentiles, opts MetricsDashboardOptions) map[string]int {
	layout := dashboardLayout(opts.Days)
	var bucketOrder []string
	buckets := make(map[string]*bucketAcc)
	stopCounts := make(map[string]int)
	validDurationCount, validThroughputCount := 0, 0
	agents := make(map[string]*AgentMetrics)
	s := &dashboard.Summary
	for i := range groups {
		g := &groups[i]
		p := g.price(opts.Pricing)
		s.Requests += g.messages
		s.TotalTokens += g.in + g.out
		s.InputTokens += g.in
		s.OutputTokens += g.out
		s.CacheReadTokens += g.read
		s.CacheWriteTokens += g.write
		s.TotalCost += g.cost
		s.TotalCalcCost += p.calc
		s.EstimatedCostByType.Input += p.byType.Input
		s.EstimatedCostByType.Output += p.byType.Output
		s.EstimatedCostByType.CacheRead += p.byType.CacheRead
		s.EstimatedCostByType.CacheWrite += p.byType.CacheWrite
		s.TotalEffectiveCost += p.effective
		s.CompletedRequests += g.completed
		s.ErrorRequests += g.errors
		s.SuccessfulRequests += g.successes
		s.AvgDurationMs += float64(g.duration)
		s.TotalDurationMs += g.duration
		validDurationCount += g.timed
		s.AvgTokensPerSec += g.tps
		validThroughputCount += g.throughput
		reason := g.finish
		if reason == "" {
			reason = "none"
		}
		stopCounts[reason] += g.messages

		b, ok := buckets[g.label]
		if !ok {
			b = &bucketAcc{label: g.label, costByModel: map[string]float64{}, estimatedCostByModel: map[string]float64{}}
			buckets[g.label] = b
			bucketOrder = append(bucketOrder, g.label)
		}
		b.inputTokens += g.in
		b.cacheReadTokens += g.read
		b.cacheWriteTokens += g.write
		b.outputTokens += g.out
		b.totalOutputTokSec += g.tps
		b.throughputCount += g.throughput
		b.totalDurationMs += float64(g.duration)
		b.durationCount += g.timed
		b.completedRequests += g.completed
		b.errorRequests += g.errors
		b.successfulRequests += g.successes
		b.totalCacheEff += g.cacheEfficiency
		b.totalCost += g.cost
		b.totalCalcCost += p.calc
		b.totalEffectiveCost += p.effective
		b.costByModel[g.model] += p.effective
		b.estimatedCostByModel[g.model] += p.calc
		b.count += g.messages

		agent := agents[g.agent]
		if agent == nil {
			agent = &AgentMetrics{Agent: g.agent}
			agents[g.agent] = agent
		}
		agent.Requests += g.messages
		agent.InputTokens += g.in
		agent.OutputTokens += g.out
		agent.TotalTokens += g.in + g.out
		agent.TotalDurationMs += g.duration
		agent.AgentDurationMs += g.agentMs
		agent.ToolDurationMs += g.toolMs
		agent.UnknownDurationMs += g.unknownMs
		agent.EffectiveCost += p.effective
		agent.ErrorRequests += g.errors
		agent.SuccessfulRequests += g.successes
	}
	for label, b := range buckets {
		p := byLabel[label]
		b.p50, b.p95 = p.p50, p.p95
	}
	if validDurationCount > 0 {
		s.AvgDurationMs /= float64(validDurationCount)
	}
	if validThroughputCount > 0 {
		s.AvgTokensPerSec /= float64(validThroughputCount)
	}
	if tc := s.CacheReadTokens + s.CacheWriteTokens; tc > 0 {
		s.CacheHitRate = float64(s.CacheReadTokens) / float64(tc)
	}
	s.P50DurationMs, s.P95DurationMs = float64(overall.p50), float64(overall.p95)
	if outcomes := s.SuccessfulRequests + s.ErrorRequests; outcomes > 0 {
		s.ErrorRate = float64(s.ErrorRequests) / float64(outcomes)
	}
	if s.SuccessfulRequests > 0 {
		s.CostPerSuccessfulRequest = s.TotalEffectiveCost / float64(s.SuccessfulRequests)
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
	// Labels sort chronologically; all-time mode emits them in that order.
	sort.Strings(bucketOrder)
	dashboard.Series = buildDashboardSeries(buckets, bucketOrder, layout, opts.Days, opts.Since)
	effective := func(b *bucketAcc) map[string]float64 { return b.costByModel }
	estimated := func(b *bucketAcc) map[string]float64 { return b.estimatedCostByModel }
	dashboard.CostByModel = buildModelCostSeries(buckets, dashboard.Series, effective, false)
	dashboard.DailyEstimatedCostByModel = buildModelCostSeries(buckets, dashboard.Series, estimated, true)
	dashboard.DailyEffectiveCostByModel = buildModelCostSeries(buckets, dashboard.Series, effective, true)
	return stopCounts
}

func stopReasonCounts(counts map[string]int) []StopReasonCount {
	var out []StopReasonCount
	for reason, count := range counts {
		out = append(out, StopReasonCount{Reason: reason, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// groupLogs accumulates groups by key in first-request order. Titles and
// directories come from the first group of each key, as the request log does.
func groupLogs(ctx context.Context, groups []dashboardGroup, opts MetricsDashboardOptions, key func(*dashboardGroup, string) string) ([]string, map[string]*logAcc, error) {
	ids := map[[2]string]string{}
	var order []string
	accs := make(map[string]*logAcc)
	for i := range groups {
		g := &groups[i]
		native := [2]string{g.instance, g.session}
		id, ok := ids[native]
		if !ok {
			id = g.session
			if opts.SessionIdentity != nil {
				var err error
				if id, err = opts.SessionIdentity(ctx, g.instance, g.session); err != nil {
					return nil, nil, err
				}
			}
			ids[native] = id
		}
		k := key(g, id)
		acc, ok := accs[k]
		if !ok {
			acc = &logAcc{title: g.title, directory: g.directory, first: g.first, last: g.last,
				agents: map[string]struct{}{}, models: map[string]struct{}{}, sessions: map[string]struct{}{}}
			accs[k] = acc
			order = append(order, k)
		}
		p := g.price(opts.Pricing)
		acc.requests += g.messages
		acc.in += g.in
		acc.out += g.out
		acc.cacheRead += g.read
		acc.cacheWrite += g.write
		acc.duration += g.duration
		acc.cost += g.cost
		acc.calcCost += p.calc
		acc.effectiveCost += p.effective
		acc.tokPerSec += g.tps
		acc.durationCount += g.throughput
		acc.first = min(acc.first, g.first)
		acc.last = max(acc.last, g.last)
		if g.agent != "" {
			acc.agents[g.agent] = struct{}{}
		}
		if g.model != "" {
			acc.models[g.model] = struct{}{}
		}
		acc.sessions[id] = struct{}{}
		acc.errors += g.errors
	}
	return order, accs, nil
}

// sessionLog aggregates groups by (identity-mapped) session, sorted by cost.
func sessionLog(ctx context.Context, groups []dashboardGroup, opts MetricsDashboardOptions) ([]SessionLogEntry, error) {
	order, accs, err := groupLogs(ctx, groups, opts, func(_ *dashboardGroup, id string) string { return id })
	if err != nil {
		return nil, err
	}
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
	sort.SliceStable(entries, func(i, j int) bool {
		return costDescLess(entries[i].Cost, entries[j].Cost, entries[i].LastRequestTime, entries[j].LastRequestTime)
	})
	return entries, nil
}

// projectLog aggregates groups by session directory, sorted by cost.
func projectLog(ctx context.Context, groups []dashboardGroup, opts MetricsDashboardOptions) ([]ProjectLogEntry, error) {
	order, accs, err := groupLogs(ctx, groups, opts, func(g *dashboardGroup, _ string) string { return g.directory })
	if err != nil {
		return nil, err
	}
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
	sort.SliceStable(entries, func(i, j int) bool {
		return costDescLess(entries[i].Cost, entries[j].Cost, entries[i].LastRequestTime, entries[j].LastRequestTime)
	})
	return entries, nil
}
