package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

type toolTiming = analyticsdata.ToolTiming

// Read the compact mirror projection, falling back to OpenCode's parts only
// when the message scan also reads OpenCode (e.g. before the initial build).
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
		if d.mirror != nil && source == d.mirror.db {
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
			timing := analyticsdata.SummarizeTiming(entry.modelStarted, entry.DurationMs, entry.OutputTokens, entry.StopReason, timings[entry.ID])
			entry.agentDurationMs, entry.toolDurationMs, entry.unknownDurationMs = timing.AgentMs, timing.ToolMs, timing.UnknownMs
			entry.TokensPerSecond = timing.TokensPerSecond
		}
	}
	return nil
}

// Reconcile timings in the message-copy window, independently of message data:
// a tool may finish without its message changing. Null timings stay unknown.
func (d *DB) copyToolTimings(ctx context.Context, tx *sql.Tx, since int64) error {
	query := `SELECT p.message_id, CAST(json_extract(p.data, '$.state.time') AS TEXT), COALESCE(json_extract(p.data, '$.tool'), '')
		FROM ` + d.messagesFrom(since, false) + ` JOIN part p ON p.message_id = m.id
		WHERE json_extract(m.data, '$.role') = 'assistant' AND json_extract(p.data, '$.type') = 'tool'`
	var args []any
	if since > 0 {
		query += ` AND m.time_created >= ?`
		args = append(args, since)
		_, err := reconcileMirrorRows(ctx, d.db, tx, query+` ORDER BY p.message_id, 2, 3`, args,
			`SELECT t.message_id, t.time, t.tool FROM tool_timing t JOIN message m ON m.id = t.message_id
			WHERE m.time_created >= ? ORDER BY t.message_id, t.time, t.tool`,
			`INSERT INTO tool_timing (message_id, time, tool)
			SELECT ?1, ?2, ?3 WHERE EXISTS (SELECT 1 FROM message WHERE id = ?1)`,
			`DELETE FROM tool_timing WHERE message_id = ?`, 3)
		return err
	}
	return copyRows(ctx, d.db, tx, query, args,
		`INSERT INTO tool_timing (message_id, time, tool) VALUES (?, ?, ?)`, 3)
}
