package statev2

import (
	"context"
	"database/sql"
	"errors"
)

// Read only compact accounting projections, never all message/part payloads.
func conversationTotals(ctx context.Context, tx *sql.Tx, key Key, d *Detail) error {
	d.ConversationTotals = map[string]ConversationTotals{}
	for _, session := range d.Tree {
		var total ConversationTotals
		err := tx.QueryRowContext(ctx, `SELECT user_messages,input_tokens,output_tokens,cost FROM analytics_session WHERE instance_id=? AND id=?`, key.InstanceID, session.SessionID).
			Scan(&total.Messages, &total.Input, &total.Output, &total.Cost)
		if err != nil {
			return err
		}
		var role, finish, errorName sql.NullString
		var hasError bool
		err = tx.QueryRowContext(ctx, `SELECT role,finish,error_name,has_error FROM message WHERE instance_id=? AND session_id=? AND source_deleted=0 AND conversation_visible=1 ORDER BY position DESC,id DESC LIMIT 1`, key.InstanceID, session.SessionID).Scan(&role, &finish, &errorName, &hasError)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		total.Role, total.Finish, total.Error = role.String, finish.String, errorName.String
		if hasError {
			total.Error = "true"
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(CASE WHEN role='assistant' THEN completed_at ELSE 0 END),0),
			COALESCE(max(CASE WHEN role='user' THEN time_created ELSE 0 END),0),COALESCE(sum(CASE WHEN role='assistant' THEN duration_ms ELSE 0 END),0)
			FROM analytics_fact WHERE instance_id=? AND session_id=?`, key.InstanceID, session.SessionID).Scan(&total.CompletedAt, &total.PromptAt, &total.ActiveDuration); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT provider,model,sum(input_tokens),sum(output_tokens),sum(cache_read),sum(cache_write),
			sum(CASE WHEN cost>0 THEN cost ELSE 0 END),sum(CASE WHEN cost<=0 THEN cost ELSE 0 END)
			FROM analytics_fact WHERE instance_id=? AND session_id=? AND role='assistant' GROUP BY provider,model,cost>0`, key.InstanceID, session.SessionID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var model ConversationModelTotal
			if err := rows.Scan(&model.Provider, &model.Model, &model.Input, &model.Output, &model.CacheRead, &model.CacheWrite, &model.Paid, &model.Unpaid); err != nil {
				rows.Close()
				return err
			}
			total.Models = append(total.Models, model)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		d.ConversationTotals[session.SessionID] = total
	}
	return nil
}
