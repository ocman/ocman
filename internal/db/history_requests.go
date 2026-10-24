package db

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

func (d *DB) scanHistoryRequests(ctx context.Context, opts MetricsDashboardOptions) ([]requestRow, map[string]struct{}, map[string]struct{}, error) {
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
	query += ` ORDER BY m.time_created ASC,m.id ASC`
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
		priceRequest(&r, opts.Pricing)
		result = append(result, r)
	}
	return result, agents, models, rows.Err()
}

func priceRequest(entry *requestRow, pricing CostCalculator) {
	if pricing != nil {
		entry.CalcCost = pricing.CalcCost(entry.Model, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, entry.CacheWriteTokens)
		entry.estimatedCostByType = CostByType{
			Input: pricing.CalcCost(entry.Model, entry.InputTokens, 0, 0, 0), Output: pricing.CalcCost(entry.Model, 0, entry.OutputTokens, 0, 0),
			CacheRead: pricing.CalcCost(entry.Model, 0, 0, entry.CacheReadTokens, 0), CacheWrite: pricing.CalcCost(entry.Model, 0, 0, 0, entry.CacheWriteTokens),
		}
	}
	entry.EffectiveCost = effectiveCost(entry.Cost, entry.CalcCost)
}

// GetStats returns aggregate statistics from retained history.
func (d *DB) GetStats(ctx context.Context) (*Stats, error) {
	r := &Stats{}
	err := d.reads().QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(instr(title,'@')>0),0),count(DISTINCT directory),
		COALESCE(sum(user_messages),0),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cost),0) FROM session`).
		Scan(&r.TotalSessions, &r.SubagentSessions, &r.TotalProjects, &r.TotalMessages, &r.TotalTokensIn, &r.TotalTokensOut, &r.TotalCost)
	return r, err
}
