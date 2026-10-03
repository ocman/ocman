package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type toolTiming struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// modelDuration excludes the union of tool intervals, including time tools
// spend awaiting permission/question answers. Startup/prefill still count:
// these timestamps support request throughput, not provider-side decode TPS.
func modelDuration(start, end int64, tools []toolTiming) int64 {
	if start <= 0 || end <= start {
		return 0
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Start < tools[j].Start })
	cursor, waiting := start, int64(0)
	for _, tool := range tools {
		if tool.Start <= 0 || tool.End <= 0 || tool.End < tool.Start {
			return 0 // Incomplete timing is unknown, not zero waiting.
		}
		from, to := max(start, tool.Start), min(end, tool.End)
		waiting += max(int64(0), to-max(cursor, from))
		cursor = max(cursor, to)
	}
	return end - start - waiting
}

// Tool payloads stay in the source DB, not the compact analytics mirror.
// Read only timing fields in bounded batches using the message_id index.
func (d *DB) applyThroughput(ctx context.Context, requests []requestRow) error {
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
		rows, err := d.db.QueryContext(ctx, query, args...)
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
			tools := timings[entry.ID]
			if entry.StopReason == "tool-calls" && len(tools) == 0 {
				continue
			}
			duration := modelDuration(entry.modelStarted, entry.modelStarted+entry.DurationMs, tools)
			if duration > 100 {
				entry.TokensPerSecond = float64(entry.OutputTokens) / (float64(duration) / 1000)
			}
		}
	}
	return nil
}
