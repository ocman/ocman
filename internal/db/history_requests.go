package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ocman/ocman/internal/analyticsdata"
)

// requestPage returns one page of the filtered request log, most recent
// first, paginated in SQLite. groups are the window's filtered request groups;
// their raw agent/provider/model combinations express the agent/model filter
// with the same trimming and model-key rules as the summaries.
func (d *DB) requestPage(ctx context.Context, opts MetricsDashboardOptions, groups []dashboardGroup) ([]RequestLogEntry, error) {
	out := []RequestLogEntry{}
	if len(groups) == 0 {
		return out, nil
	}
	query := `SELECT m.instance_id,m.id,m.session_id,m.time_created,m.agent,m.provider,m.model,m.cost,
		m.input_tokens,m.output_tokens,m.cache_read,m.cache_write,m.duration_ms,m.tokens_per_second,m.finish
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
	if opts.AgentFilter != "" || opts.ModelFilter != "" {
		combos, err := json.Marshal(groupCombos(groups))
		if err != nil {
			return nil, err
		}
		query += ` AND EXISTS(SELECT 1 FROM json_each(?) c WHERE json_extract(c.value,'$[0]')=m.agent
			AND json_extract(c.value,'$[1]')=m.provider AND json_extract(c.value,'$[2]')=m.model)`
		args = append(args, string(combos))
	}
	limit := int64(opts.RequestLimit)
	if limit <= 0 {
		limit = -1
	}
	// Session and source break ties: native message IDs are session-local.
	query += ` ORDER BY m.time_created DESC,m.id DESC,m.session_id DESC,m.instance_id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, max(0, opts.RequestOffset))
	rows, err := d.reads().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r RequestLogEntry
		var instance, provider, model string
		if err := rows.Scan(&instance, &r.ID, &r.SessionID, &r.TimeCreated, &r.Agent, &provider, &model, &r.Cost,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.DurationMs, &r.TokensPerSecond, &r.StopReason); err != nil {
			return nil, err
		}
		r.Agent = strings.TrimSpace(r.Agent)
		r.Model = analyticsdata.ModelKey(provider, strings.TrimSpace(model))
		if opts.SessionIdentity != nil {
			if r.SessionID, err = opts.SessionIdentity(ctx, instance, r.SessionID); err != nil {
				return nil, err
			}
		}
		if r.StopReason == "" {
			r.StopReason = "none"
		}
		if opts.Pricing != nil {
			r.CalcCost = opts.Pricing.CalcCost(r.Model, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens)
		}
		r.EffectiveCost = effectiveCost(r.Cost, r.CalcCost)
		out = append(out, r)
	}
	return out, rows.Err()
}

// groupCombos lists the distinct raw agent/provider/model values of groups.
func groupCombos(groups []dashboardGroup) [][3]string {
	seen := map[[3]string]bool{}
	var combos [][3]string
	for _, g := range groups {
		key := [3]string{g.rawAgent, g.provider, g.rawModel}
		if !seen[key] {
			seen[key] = true
			combos = append(combos, key)
		}
	}
	return combos
}

// GetStats returns aggregate statistics from retained history.
func (d *DB) GetStats(ctx context.Context) (*Stats, error) {
	r := &Stats{}
	err := d.reads().QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(instr(title,'@')>0),0),count(DISTINCT directory),
		COALESCE(sum(user_messages),0),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cost),0) FROM session`).
		Scan(&r.TotalSessions, &r.SubagentSessions, &r.TotalProjects, &r.TotalMessages, &r.TotalTokensIn, &r.TotalTokensOut, &r.TotalCost)
	return r, err
}
