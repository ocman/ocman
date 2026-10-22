package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

type toolTiming = analyticsdata.ToolTiming

// Read tool timings from the same source as the message scan.
func (d *DB) applyThroughput(ctx context.Context, source querier, requests []requestRow) error {
	const batchSize = 400
	for offset := 0; offset < len(requests); offset += batchSize {
		batch := requests[offset:min(offset+batchSize, len(requests))]
		args := make([]any, len(batch))
		for i := range batch {
			args[i] = batch[i].ID
		}
		query := `SELECT message_id, json_extract(data, '$.state.time') FROM part
			WHERE message_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + `)
			AND json_extract(data, '$.type') = 'tool'`
		rows, err := source.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("reading throughput timings: %w", err)
		}
		timings := make(map[string][]toolTiming)
		for rows.Next() {
			var id string
			var raw *string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var timing toolTiming
			if raw != nil {
				// Malformed/missing timing suppresses this sample.
				if err := json.Unmarshal([]byte(*raw), &timing); err != nil {
					timing = toolTiming{}
				}
			}
			timings[id] = append(timings[id], timing)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i := range batch {
			entry := &batch[i]
			timing := analyticsdata.SummarizeTiming(entry.modelStarted, entry.DurationMs, entry.OutputTokens, entry.StopReason, timings[entry.ID])
			entry.agentDurationMs, entry.toolDurationMs, entry.unknownDurationMs = timing.AgentMs, timing.ToolMs, timing.UnknownMs
			entry.TokensPerSecond = timing.TokensPerSecond
		}
	}
	return nil
}
