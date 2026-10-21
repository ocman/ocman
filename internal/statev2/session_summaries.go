package statev2

import (
	"context"
	"database/sql"
	"fmt"
)

// SessionSummary is a compact, instance-scoped read model. It carries persisted
// facts only; running and pending-prompt signals must come from platform events.
type SessionSummary struct {
	Session
	Totals               ConversationTotals
	LatestMessageID      string
	LatestMessageCreated int64
	ErrorMessage         string
}

// SessionSummaries reads metadata and indexed accounting without loading
// conversation content. A specific ID can read retained source-deleted history;
// inventories omit those rows. Both queries share one consistent transaction.
func (d *DB) SessionSummaries(ctx context.Context, instance, directory, id string) ([]SessionSummary, error) {
	if instance == "" {
		return nil, fmt.Errorf("statev2: session instance is required")
	}
	tx, err := d.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `WITH timings AS (
		SELECT session_id,max(CASE WHEN role='assistant' THEN completed_at ELSE 0 END) AS completed,
		max(CASE WHEN role='user' THEN time_created ELSE 0 END) AS prompted,
		sum(CASE WHEN role='assistant' THEN duration_ms ELSE 0 END) AS active
		FROM analytics_fact WHERE instance_id=? AND (?='' OR session_id=?) GROUP BY session_id)
		SELECT s.id,s.title,s.directory,s.parent_id,s.project_id,s.created_at,s.updated_at,s.source_deleted,s.revision,s.extensions,s.read_format,s.is_subagent,
		a.user_messages,a.input_tokens,a.output_tokens,a.cost,COALESCE(t.completed,0),COALESCE(t.prompted,0),COALESCE(t.active,0),
		COALESCE(m.id,''),COALESCE(m.created_at,0),COALESCE(m.role,''),COALESCE(m.finish,''),
		CASE WHEN m.has_error=1 THEN COALESCE(NULLIF(m.error_name,''),'true') ELSE '' END,
		COALESCE(json_extract(am.data,'$.error.data.message'),'')
		FROM session s JOIN analytics_session a ON a.instance_id=s.instance_id AND a.id=s.id
		LEFT JOIN timings t ON t.session_id=s.id
		LEFT JOIN message m ON m.instance_id=s.instance_id AND m.session_id=s.id AND m.id=(
			SELECT id FROM message WHERE instance_id=s.instance_id AND session_id=s.id AND source_deleted=0 AND conversation_visible=1 ORDER BY position DESC,id DESC LIMIT 1)
		LEFT JOIN analytics_message am ON am.instance_id=m.instance_id AND am.session_id=m.session_id AND am.id=m.id
		WHERE s.instance_id=? AND (?='' OR s.directory=?) AND ((?='' AND s.source_deleted=0) OR s.id=?)
		ORDER BY s.updated_at DESC,s.id`, instance, id, id, instance, directory, directory, id, id)
	if err != nil {
		return nil, err
	}
	out := []SessionSummary{}
	index := map[string]int{}
	for rows.Next() {
		var s SessionSummary
		var ext string
		s.InstanceID = instance
		if err := rows.Scan(&s.SessionID, &s.Title, &s.Directory, &s.ParentID, &s.ProjectID, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &ext, &s.ReadFormat, &s.Subagent,
			&s.Totals.Messages, &s.Totals.Input, &s.Totals.Output, &s.Totals.Cost, &s.Totals.CompletedAt, &s.Totals.PromptAt, &s.Totals.ActiveDuration,
			&s.LatestMessageID, &s.LatestMessageCreated, &s.Totals.Role, &s.Totals.Finish, &s.Totals.Error, &s.ErrorMessage); err != nil {
			rows.Close()
			return nil, err
		}
		s.Extensions = []byte(ext)
		index[s.SessionID] = len(out)
		out = append(out, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	models, err := tx.QueryContext(ctx, `SELECT f.session_id,f.provider,f.model,sum(f.input_tokens),sum(f.output_tokens),sum(f.cache_read),sum(f.cache_write),
		sum(CASE WHEN f.cost>0 THEN f.cost ELSE 0 END),sum(CASE WHEN f.cost<=0 THEN f.cost ELSE 0 END)
		FROM analytics_fact f JOIN session s ON s.instance_id=f.instance_id AND s.id=f.session_id
		WHERE s.instance_id=? AND (?='' OR s.directory=?) AND ((?='' AND s.source_deleted=0) OR s.id=?) AND f.role='assistant'
		GROUP BY f.session_id,f.provider,f.model,f.cost>0 ORDER BY f.session_id,f.provider,f.model,f.cost>0`, instance, directory, directory, id, id)
	if err != nil {
		return nil, err
	}
	for models.Next() {
		var session string
		var model ConversationModelTotal
		if err := models.Scan(&session, &model.Provider, &model.Model, &model.Input, &model.Output, &model.CacheRead, &model.CacheWrite, &model.Paid, &model.Unpaid); err != nil {
			models.Close()
			return nil, err
		}
		if i, ok := index[session]; ok {
			out[i].Totals.Models = append(out[i].Totals.Models, model)
		}
	}
	err = models.Err()
	models.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
