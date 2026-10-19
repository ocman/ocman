package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
)

// projectAnalytics writes small, legacy-shaped rows alongside the retained
// full payload. Existing calculations use these rows without parsing tool
// outputs, attachments, or native platform envelopes. No second sync exists.
func projectAnalytics(ctx context.Context, tx *sql.Tx, b Batch) error {
	s := b.Session
	if b.Complete {
		for _, table := range []string{"analytics_message", "analytics_part"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE instance_id=? AND session_id=?", s.InstanceID, s.SessionID); err != nil {
				return err
			}
		}
	}
	for _, m := range b.Messages {
		if m.SourceDeleted {
			if _, err := tx.ExecContext(ctx, `DELETE FROM analytics_message WHERE instance_id=? AND session_id=? AND id=?`, s.InstanceID, s.SessionID, m.ID); err != nil {
				return err
			}
			continue
		}
		data, err := analyticsMetadata(m)
		if err != nil {
			return err
		}
		settled := m.Role != "assistant" || m.CompletedAt != 0 || m.Finish != "" || m.HasError
		if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_message VALUES(?,?,?,?,?,?)
			ON CONFLICT(instance_id,session_id,id) DO UPDATE SET time_created=excluded.time_created,settled=excluded.settled,data=excluded.data`,
			s.InstanceID, s.SessionID, m.ID, m.CreatedAt, settled, string(data)); err != nil {
			return err
		}
	}
	for _, p := range b.Parts {
		// A kind change or explicit source deletion also removes old timing.
		if _, err := tx.ExecContext(ctx, `DELETE FROM analytics_part WHERE instance_id=? AND session_id=? AND message_id=? AND id=?`, s.InstanceID, s.SessionID, p.MessageID, p.ID); err != nil {
			return err
		}
		if p.SourceDeleted || p.Kind != "tool" {
			continue
		}
		var fields struct {
			Tool  string `json:"tool"`
			State struct {
				Time json.RawMessage `json:"time"`
			} `json:"state"`
		}
		if err := json.Unmarshal([]byte(jsonValue(p.Extensions)), &fields); err != nil {
			return err
		}
		// Missing, null, or incomplete timings stay unknown, not zero duration.
		data, err := json.Marshal(map[string]any{"type": "tool", "tool": fields.Tool, "state": map[string]any{"time": fields.State.Time}})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_part VALUES(?,?,?,?,?)`, s.InstanceID, s.SessionID, p.MessageID, p.ID, string(data)); err != nil {
			return err
		}
	}
	return nil
}

func analyticsMetadata(m Message) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonValue(m.Extensions)), &raw); err != nil {
		return nil, err
	}
	if metadata, ok := raw["metadata"]; ok {
		if err := json.Unmarshal(metadata, &raw); err != nil {
			return nil, err
		}
	}
	fields := map[string]json.RawMessage{}
	for _, key := range []string{"role", "agent", "mode", "providerID", "modelID", "model", "cost", "usageAvailable", "tokens", "time", "finish", "error"} {
		if value, ok := raw[key]; ok {
			fields[key] = value
		}
	}
	if _, ok := fields["role"]; !ok {
		fields["role"], _ = json.Marshal(m.Role)
	}
	return json.Marshal(fields)
}
