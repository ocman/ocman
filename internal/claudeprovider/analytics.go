package claudeprovider

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"github.com/NoUseFreak/ocman/internal/db"
)

// analyticsRows projects converted messages the way analytics-cache.db
// stores OpenCode's: assistant JSON as converted, every other role as
// {"role": ...}. Cost stays 0, so analytics price the tokens at API rates.
func analyticsRows(messages []db.Message, tools map[string][]string) []db.AnalyticsMessage {
	out := make([]db.AnalyticsMessage, 0, len(messages))
	for _, m := range messages {
		var info struct {
			Role   string          `json:"role"`
			Finish string          `json:"finish"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(m.Data, &info) != nil {
			continue
		}
		row := db.AnalyticsMessage{ID: m.ID, SessionID: m.SessionID, TimeCreated: m.TimeCreated, Settled: true, Data: string(m.Data)}
		if info.Role != "assistant" {
			data, _ := json.Marshal(map[string]string{"role": info.Role})
			row.Data = string(data)
		} else {
			row.Settled = info.Finish != "" || len(info.Error) > 0
			row.ToolTimes = tools[m.ID]
		}
		out = append(out, row)
	}
	return out
}

// toolTimes records each converted tool part's state.time JSON under its
// message, the timing analytics-cache.db's tool_timing keeps for OpenCode.
func toolTimes(parts []db.Part, into map[string][]string) {
	for _, p := range parts {
		var part struct {
			Type  string `json:"type"`
			State struct {
				Time json.RawMessage `json:"time"`
			} `json:"state"`
		}
		if json.Unmarshal(p.Data, &part) != nil || part.Type != "tool" {
			continue
		}
		into[p.MessageID] = append(into[p.MessageID], string(part.State.Time))
	}
}

// AnalyticsSource feeds the index's sessions and messages into
// analytics-cache.db (AD-12): messages created at or after since, plus those
// of transcripts (re)indexed at or after changedSince. It opens
// claude-index.db read-only per call; a missing index contributes nothing.
func AnalyticsSource(indexPath string) db.AnalyticsSource {
	return func(ctx context.Context, since, changedSince int64) ([]db.AnalyticsSession, []db.AnalyticsMessage, error) {
		if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		conn, err := sql.Open("sqlite", "file:"+indexPath+"?mode=ro&_pragma=busy_timeout(5000)")
		if err != nil {
			return nil, nil, err
		}
		defer conn.Close()
		var sessions []db.AnalyticsSession
		rows, err := conn.QueryContext(ctx, `SELECT id, parent_id, directory, title, time_created FROM session`)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var s db.AnalyticsSession
			if err := rows.Scan(&s.ID, &s.ParentID, &s.Directory, &s.Title, &s.TimeCreated); err != nil {
				rows.Close()
				return nil, nil, err
			}
			sessions = append(sessions, s)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		var messages []db.AnalyticsMessage
		rows, err = conn.QueryContext(ctx, `SELECT id, session_id, time_created, settled, data, tools FROM usage
			WHERE time_created >= ? OR indexed >= ?`, since, changedSince)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var m db.AnalyticsMessage
			var tools string
			if err := rows.Scan(&m.ID, &m.SessionID, &m.TimeCreated, &m.Settled, &m.Data, &tools); err != nil {
				return nil, nil, err
			}
			_ = json.Unmarshal([]byte(tools), &m.ToolTimes)
			messages = append(messages, m)
		}
		return sessions, messages, rows.Err()
	}
}
