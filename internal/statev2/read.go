package statev2

import (
	"context"
	"database/sql"
	"errors"
)

func readDetail(ctx context.Context, tx *sql.Tx, key Key, limit, offset int) (*Detail, error) {
	detail := &Detail{Messages: []Message{}, Parts: []Part{}}
	s := &detail.Session
	s.Key = key
	var extensions string
	err := tx.QueryRowContext(ctx, `SELECT title,directory,parent_id,project_id,created_at,updated_at,source_deleted,revision,extensions,read_format,is_subagent
		FROM session WHERE instance_id=? AND id=?`, key.InstanceID, key.SessionID).
		Scan(&s.Title, &s.Directory, &s.ParentID, &s.ProjectID, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &extensions, &s.ReadFormat, &s.Subagent)
	if err != nil {
		return nil, notFound(err)
	}
	s.Extensions = []byte(extensions)
	const visible = ` AND source_deleted=0 AND conversation_visible=1`
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM message WHERE instance_id=? AND session_id=?`+visible,
		key.InstanceID, key.SessionID).Scan(&detail.TotalMessages); err != nil {
		return nil, err
	}
	end := max(0, detail.TotalMessages-offset)
	offset = max(0, end-limit)
	limit = min(limit, end)
	err = tx.QueryRowContext(ctx, `SELECT value FROM source_state WHERE instance_id=? AND key=?`, key.InstanceID, "read-ready/"+key.SessionID).Scan(&detail.ReadyVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,position,role,provider,model,created_at,completed_at,cost,tokens_input,tokens_output,
		tokens_reasoning,tokens_cache_read,tokens_cache_write,finish,has_error,error_name,agent,source_deleted,extensions,conversation_visible
		FROM message WHERE instance_id=? AND session_id=?`+visible+` ORDER BY position,id LIMIT ? OFFSET ?`,
		key.InstanceID, key.SessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m Message
		var raw string
		if err := rows.Scan(&m.ID, &m.Position, &m.Role, &m.Provider, &m.Model, &m.CreatedAt, &m.CompletedAt, &m.Cost,
			&m.TokensInput, &m.TokensOutput, &m.TokensReasoning, &m.TokensCacheRead, &m.TokensCacheWrite, &m.Finish,
			&m.HasError, &m.ErrorName, &m.Agent, &m.SourceDeleted, &raw, &m.ConversationVisible); err != nil {
			rows.Close()
			return nil, err
		}
		m.Extensions = []byte(raw)
		detail.Messages = append(detail.Messages, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT p.id,p.message_id,p.position,p.kind,p.text,p.source_deleted,p.extensions,p.created_at,p.text_present
		FROM part p JOIN (SELECT id,position FROM message WHERE instance_id=? AND session_id=?`+visible+`
		ORDER BY position,id LIMIT ? OFFSET ?) m ON m.id=p.message_id
		WHERE p.instance_id=? AND p.session_id=? AND p.source_deleted=0 ORDER BY m.position,m.id,p.position,p.id`,
		key.InstanceID, key.SessionID, limit, offset, key.InstanceID, key.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Part
		var raw string
		if err := rows.Scan(&p.ID, &p.MessageID, &p.Position, &p.Kind, &p.Text, &p.SourceDeleted, &raw, &p.CreatedAt, &p.TextPresent); err != nil {
			return nil, err
		}
		p.Extensions = []byte(raw)
		detail.Parts = append(detail.Parts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return detail, nil
}
