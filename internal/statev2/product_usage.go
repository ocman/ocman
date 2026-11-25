package statev2

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/usagewire"
)

// ProductUsageDay reads only the compact assistant analytics projection for
// ready, owner-local instances. Session keys are used for local deduplication
// and never returned. day is the UTC midnight of a closed day.
func (d *DB) ProductUsageDay(ctx context.Context, instances []string, day time.Time) (usagewire.Daily, error) {
	var result usagewire.Daily
	if len(instances) == 0 {
		return result, nil
	}
	args := []any{day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()}
	for _, id := range instances {
		args = append(args, id)
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT instance_id,session_id,model,sum(input_tokens),sum(output_tokens)
		FROM analytics_hour WHERE bucket>=? AND bucket<? AND role='assistant' AND valid=1
		AND instance_id IN (?`+strings.Repeat(",?", len(instances)-1)+`)
		GROUP BY instance_id,session_id,model ORDER BY instance_id,session_id,model`, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	models := map[string]usagewire.ModelUsage{}
	seen := map[string]bool{}
	var previous Key
	for rows.Next() {
		var session Key
		var rawModel string
		var input, output int64
		if err := rows.Scan(&session.InstanceID, &session.SessionID, &rawModel, &input, &output); err != nil {
			return usagewire.Daily{}, err
		}
		if result.Sessions == 0 || previous != session {
			result.Sessions++
			clear(seen)
			previous = session
		}
		model := usagewire.ModelBucket(rawModel)
		m := models[model]
		m.Model = model
		if !seen[model] {
			m.Sessions++
			seen[model] = true
		}
		m.InputTokens += input
		m.OutputTokens += output
		models[model] = m
	}
	for _, m := range models {
		result.Models = append(result.Models, m)
	}
	slices.SortFunc(result.Models, func(a, b usagewire.ModelUsage) int { return strings.Compare(a.Model, b.Model) })
	return result, rows.Err()
}
