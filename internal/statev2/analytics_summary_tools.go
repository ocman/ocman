package statev2

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

func summarizeScopedTools(ctx context.Context, tx *sql.Tx, key Key, scope summaryScope) (map[string][]analyticsdata.ToolTiming, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,message_id,data FROM analytics_part WHERE instance_id=? AND session_id=?`+scope.parts, scope.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type question struct {
		id, message string
		start, end  int64
	}
	var questions []question
	tools := map[string][]analyticsdata.ToolTiming{}
	for rows.Next() {
		var id, message string
		var raw []byte
		if err := rows.Scan(&id, &message, &raw); err != nil {
			return nil, err
		}
		var part struct {
			Tool  string `json:"tool"`
			State struct {
				Time json.RawMessage `json:"time"`
			} `json:"state"`
		}
		if err := json.Unmarshal(raw, &part); err != nil {
			return nil, err
		}
		var timing analyticsdata.ToolTiming
		if err := json.Unmarshal(part.State.Time, &timing); err != nil {
			timing = analyticsdata.ToolTiming{}
		}
		tools[message] = append(tools[message], timing)
		if part.Tool == "question" {
			var nullable struct {
				Start *int64 `json:"start"`
				End   *int64 `json:"end"`
			}
			if json.Unmarshal(part.State.Time, &nullable) == nil && nullable.Start != nil && nullable.End != nil && *nullable.End > *nullable.Start {
				questions = append(questions, question{id, message, *nullable.Start, *nullable.End})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, q := range questions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_question VALUES(?,?,?,?,?,?)`, key.InstanceID, key.SessionID, q.message, q.id, q.start, q.end); err != nil {
			return nil, err
		}
	}
	return tools, nil
}

func backfillAnalyticsSummaries(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT instance_id,id FROM session`)
	if err != nil {
		return err
	}
	var keys []Key
	for rows.Next() {
		var key Key
		if err := rows.Scan(&key.InstanceID, &key.SessionID); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := rebuildAnalyticsSummary(ctx, tx, key); err != nil {
			return err
		}
	}
	return nil
}
