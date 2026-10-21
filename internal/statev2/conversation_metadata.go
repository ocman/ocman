package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

func conversationMetadata(ctx context.Context, tx *sql.Tx, key Key, d *Detail) error {
	var role, finish, errorName sql.NullString
	var hasError bool
	var latest sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT m.role,m.finish,m.error_name,m.has_error,a.data FROM message m
		LEFT JOIN analytics_message a ON a.instance_id=m.instance_id AND a.session_id=m.session_id AND a.id=m.id
		WHERE m.instance_id=? AND m.session_id=? AND m.source_deleted=0 AND m.conversation_visible=1 ORDER BY m.position DESC,m.id DESC LIMIT 1`, key.InstanceID, key.SessionID).Scan(&role, &finish, &errorName, &hasError, &latest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	d.Conversation.LatestRole, d.Conversation.LatestFinish, d.Conversation.LatestError = role.String, finish.String, errorName.String
	if hasError {
		d.Conversation.LatestError = "true"
		d.Conversation.LatestErrorName = errorName.String
		var data struct {
			Error *struct {
				Message string
				Data    *struct{ Message string }
			}
		}
		if json.Unmarshal([]byte(latest.String), &data) == nil && data.Error != nil {
			d.Conversation.LatestErrorMessage = data.Error.Message
			if data.Error.Data != nil {
				d.Conversation.LatestErrorMessage = data.Error.Data.Message
			}
		}
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM analytics_message WHERE instance_id=? AND session_id=?
		AND json_extract(data,'$.role')='assistant' AND json_extract(data,'$.tokens.output')>0 ORDER BY time_created DESC,id DESC LIMIT 1`, key.InstanceID, key.SessionID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var m analyticsdata.MessageData
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if m.Tokens != nil {
			t := m.Tokens
			d.Conversation.ContextTokens = t.Input + t.Output + t.Reasoning
			if t.Cache != nil {
				d.Conversation.ContextTokens += t.Cache.Read + t.Cache.Write
			}
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT data FROM analytics_message WHERE instance_id=? AND session_id=?
		AND json_extract(data,'$.role')='user' AND (COALESCE(json_extract(data,'$.modelID'),'')!='' OR COALESCE(json_extract(data,'$.model.modelID'),'')!='') ORDER BY time_created DESC,id DESC LIMIT 1`, key.InstanceID, key.SessionID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var m analyticsdata.MessageData
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		provider, model := m.ProviderID, m.ModelID
		if model == "" && m.Model != nil {
			provider, model = m.Model.ProviderID, m.Model.ModelID
		}
		d.Conversation.UserModel = analyticsdata.ModelKey(provider, model)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(CASE WHEN role='assistant' THEN completed_at ELSE 0 END),0),
		COALESCE(max(CASE WHEN role='user' THEN time_created ELSE 0 END),0),COALESCE(sum(CASE WHEN role='assistant' THEN duration_ms ELSE 0 END),0)
		FROM analytics_fact WHERE instance_id=? AND session_id=?`, key.InstanceID, key.SessionID).
		Scan(&d.Conversation.LastTurnCompletedAt, &d.Conversation.LastUserPromptAt, &d.Conversation.ActiveDurationMs); err != nil {
		return err
	}
	if err := conversationDefaults(ctx, tx, key, d); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE tree(id) AS (
		SELECT id FROM session WHERE instance_id=? AND id=?
		UNION SELECT s.parent_id FROM session s JOIN tree t ON s.id=t.id WHERE s.instance_id=? AND s.parent_id!='' AND COALESCE(json_extract(s.extensions,'$.SourceChild'),1)!=0
		UNION SELECT s.id FROM session s JOIN tree t ON s.parent_id=t.id WHERE s.instance_id=? AND COALESCE(json_extract(s.extensions,'$.SourceChild'),1)!=0
		) SELECT id,title,directory,parent_id,project_id,created_at,updated_at,source_deleted,revision,extensions,read_format,is_subagent FROM session
		WHERE instance_id=? AND id IN(SELECT id FROM tree) ORDER BY created_at,id`, key.InstanceID, key.SessionID, key.InstanceID, key.InstanceID, key.InstanceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s Session
		var raw string
		s.InstanceID = key.InstanceID
		if err := rows.Scan(&s.SessionID, &s.Title, &s.Directory, &s.ParentID, &s.ProjectID, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &raw, &s.ReadFormat, &s.Subagent); err != nil {
			return err
		}
		s.Extensions = []byte(raw)
		d.Tree = append(d.Tree, s)
	}
	return rows.Err()
}

func conversationDefaults(ctx context.Context, tx *sql.Tx, key Key, d *Detail) error {
	for _, directory := range []string{d.Session.Directory, ""} {
		filter := ""
		args := []any{key.InstanceID, key.SessionID}
		if directory != "" {
			filter = ` AND directory=?`
			args = append(args, directory)
		}
		query := conversationDefaultsQuery(filter)
		args = append(args, key.InstanceID)
		var raw []byte
		err := tx.QueryRowContext(ctx, query, args...).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var m analyticsdata.MessageData
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		d.Conversation.DefaultAgent = m.Agent
		provider, model := m.ProviderID, m.ModelID
		if provider != "" && model != "" {
			d.Conversation.DefaultModel = provider + "/" + model
		} else if m.Model != nil && m.Model.ProviderID != "" && m.Model.ModelID != "" {
			d.Conversation.DefaultModel = m.Model.ProviderID + "/" + m.Model.ModelID
		} else {
			if model == "" && m.Model != nil {
				model = m.Model.ModelID
			}
			d.Conversation.DefaultModel = model
		}
		return nil
	}
	return nil
}

func conversationDefaultsQuery(filter string) string {
	// Keep the five candidates as the outer loop and use the session index.
	// CROSS JOIN alone still allows the source-wide role index, scanning every
	// assistant row once per candidate instead of only that session's messages.
	return `WITH candidates AS (SELECT id,updated_at FROM session WHERE instance_id=? AND id!=? AND source_deleted=0
		AND is_subagent=0` + filter + ` ORDER BY updated_at DESC LIMIT 5)
		SELECT m.data FROM candidates c CROSS JOIN analytics_message m INDEXED BY analytics_message_session ON m.session_id=c.id WHERE m.instance_id=?
		AND json_extract(m.data,'$.role')='assistant' AND (
		COALESCE(json_extract(m.data,'$.agent'),'')!='' OR COALESCE(json_extract(m.data,'$.providerID'),'')!='' OR COALESCE(json_extract(m.data,'$.modelID'),'')!=''
		OR COALESCE(json_extract(m.data,'$.model.providerID'),'')!='' OR COALESCE(json_extract(m.data,'$.model.modelID'),'')!='')
		ORDER BY c.updated_at DESC,m.time_created DESC LIMIT 1`
}
