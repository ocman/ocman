package db

import (
	"context"
	"database/sql"
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

// Read the compact mirror projection, falling back to source payloads only
// when the message scan also uses the source (e.g. before the initial build).
func (d *DB) applyThroughput(ctx context.Context, source *sql.DB, requests []requestRow) error {
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
		if source != d.db {
			query = `SELECT message_id, time FROM tool_timing WHERE message_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + `)`
		}
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

// Reconcile timings in the message-copy window, independently of message data:
// a tool may finish without its message changing. Null timings stay unknown.
func (d *DB) copyToolTimings(ctx context.Context, tx *sql.Tx, since int64) error {
	query := `SELECT p.message_id, CAST(json_extract(p.data, '$.state.time') AS TEXT)
		FROM ` + messagesFrom(since, false) + ` JOIN part p ON p.message_id = m.id
		WHERE json_extract(m.data, '$.role') = 'assistant' AND json_extract(p.data, '$.type') = 'tool'`
	var args []any
	if since > 0 {
		query += ` AND m.time_created >= ?`
		args = append(args, since)
		_, err := reconcileMirrorRows(ctx, d.db, tx, query+` ORDER BY p.message_id, 2`, args,
			`SELECT t.message_id, t.time FROM tool_timing t JOIN message m ON m.id = t.message_id
			WHERE m.time_created >= ? ORDER BY t.message_id, t.time`,
			`INSERT INTO tool_timing (message_id, time)
			SELECT ?1, ?2 WHERE EXISTS (SELECT 1 FROM message WHERE id = ?1)`,
			`DELETE FROM tool_timing WHERE message_id = ?`, 2)
		return err
	}
	return copyRows(ctx, d.db, tx, query, args,
		`INSERT INTO tool_timing (message_id, time) VALUES (?, ?)`, 2)
}
