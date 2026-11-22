package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// readV2History uses the shared converter for supported messages, preserving
// the original payload beside every normalized message. Unlike UI TEMP views,
// history also retains system and future native message types as opaque records.
func readV2History(ctx context.Context, tx *sql.Tx, out *HistorySnapshot, only ...string) (*HistorySnapshot, error) {
	query, args := `SELECT id,type,seq,time_created,time_updated,data FROM main.session_message WHERE session_id=?`, []any{out.Session.ID}
	if only != nil {
		if len(only) == 0 {
			out.NativeMessages = map[string]json.RawMessage{}
			return out, nil
		}
		query += ` AND id IN (?` + strings.Repeat(`,?`, len(only)-1) + `)`
		for _, id := range only {
			args = append(args, id)
		}
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY seq,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out.NativeMessages = map[string]json.RawMessage{}
	for rows.Next() {
		var id, kind, raw string
		var sequence, created, updated int64
		if err := rows.Scan(&id, &kind, &sequence, &created, &updated, &raw); err != nil {
			return nil, err
		}
		converted, ok, err := convertV2HistoryMessage(out.Session.ID, id, kind, json.RawMessage(raw))
		if err != nil {
			return nil, err
		}
		original, err := json.Marshal(struct {
			Type                string
			Sequence, UpdatedAt int64
			Data                json.RawMessage
		}{kind, sequence, updated, json.RawMessage(raw)})
		if err != nil {
			return nil, err
		}
		out.NativeMessages[id] = original
		if !ok {
			// This is opaque retention, not a second native converter. Its
			// payload remains available when support is added in the future.
			out.Messages = append(out.Messages, Message{ID: id, SessionID: out.Session.ID, TimeCreated: created,
				Data: json.RawMessage(`{"role":"unknown"}`)})
			continue
		}
		data, err := json.Marshal(converted.Info)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, Message{ID: id, SessionID: out.Session.ID, TimeCreated: created, Data: data})
		for _, part := range converted.Parts {
			data, err := json.Marshal(part)
			if err != nil {
				return nil, err
			}
			pid, _ := part["id"].(string)
			out.Parts = append(out.Parts, Part{ID: pid, MessageID: id, SessionID: out.Session.ID, TimeCreated: created, Data: data})
		}
	}
	return out, rows.Err()
}
