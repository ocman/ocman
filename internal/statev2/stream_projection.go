package statev2

import (
	"context"
	"database/sql"
)

type streamProjectionChanges struct{ sidebar, analytics, metadata, attention bool }

// Compare captured inputs inside the same transaction, before any upsert.
// Live feeds include unchanged envelopes with part deltas; their presence alone
// must not cause a session-wide accounting or attention rebuild.
func projectionChanges(ctx context.Context, tx *sql.Tx, b StreamBatch) (streamProjectionChanges, error) {
	key := b.key()
	changes := streamProjectionChanges{sidebar: len(b.RemovedMessages) > 0, attention: len(b.RemovedMessages) > 0, analytics: len(b.RemovedMessages)+len(b.RemovedParts) > 0}
	var sidebar, analytics, attention bool
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM session_sidebar WHERE instance_id=? AND id=?),
		EXISTS(SELECT 1 FROM analytics_session WHERE instance_id=? AND id=?),
		EXISTS(SELECT 1 FROM sidebar_attention WHERE instance_id=? AND session_id=?)`,
		key.InstanceID, key.SessionID, key.InstanceID, key.SessionID, key.InstanceID, key.SessionID).Scan(&sidebar, &analytics, &attention); err != nil {
		return changes, err
	}
	changes.sidebar, changes.analytics = changes.sidebar || !sidebar, changes.analytics || !analytics
	changes.attention = changes.attention || !attention
	if s := b.Session; s != nil {
		var same bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session WHERE instance_id=? AND id=?
			AND title=? AND directory=? AND parent_id=? AND project_id=? AND created_at=? AND updated_at=?
			AND source_deleted=? AND extensions=? AND read_format=? AND is_subagent=?)`,
			s.InstanceID, s.SessionID, s.Title, s.Directory, s.ParentID, s.ProjectID, s.CreatedAt, s.UpdatedAt,
			s.SourceDeleted, jsonValue(s.Extensions), s.ReadFormat, s.Subagent).Scan(&same)
		if err != nil {
			return changes, err
		}
		// Session metadata is copied into the compact rows, but never changes
		// message facts, hourly accounting, tool timings, or attention maxima.
		changes.sidebar, changes.metadata = changes.sidebar || !same, !same
	}
	for _, m := range b.Messages {
		var same bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM message WHERE instance_id=? AND session_id=? AND id=?
			AND role=? AND provider=? AND model=? AND created_at=? AND completed_at=? AND cost=?
			AND tokens_input=? AND tokens_output=? AND tokens_reasoning=? AND tokens_cache_read=? AND tokens_cache_write=?
			AND finish=? AND has_error=? AND error_name=? AND agent=? AND source_deleted=? AND conversation_visible=? AND extensions=?
			AND EXISTS(SELECT 1 FROM analytics_message a WHERE a.instance_id=message.instance_id AND a.session_id=message.session_id AND a.id=message.id))`,
			key.InstanceID, key.SessionID, m.ID, m.Role, m.Provider, m.Model, m.CreatedAt, m.CompletedAt, m.Cost,
			m.TokensInput, m.TokensOutput, m.TokensReasoning, m.TokensCacheRead, m.TokensCacheWrite,
			m.Finish, m.HasError, m.ErrorName, m.Agent, m.SourceDeleted, m.ConversationVisible, jsonValue(m.Extensions)).Scan(&same)
		if err != nil {
			return changes, err
		}
		changes.sidebar, changes.analytics = changes.sidebar || !same, changes.analytics || !same
		changes.attention = changes.attention || !same
	}
	// Only tools contribute part-level analytics. A tool changing into another
	// kind must still remove its old timings; new non-tool content does not.
	if !changes.analytics {
		for _, p := range b.Parts {
			if p.Kind == "tool" {
				changes.analytics = true
				break
			}
			var wasTool bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM part WHERE instance_id=? AND session_id=? AND message_id=? AND id=? AND kind='tool')`,
				key.InstanceID, key.SessionID, p.MessageID, p.ID).Scan(&wasTool); err != nil {
				return changes, err
			}
			if wasTool {
				changes.analytics = true
				break
			}
		}
	}
	return changes, nil
}

// Copy only session-owned fields. Accounting totals remain untouched; using a
// summary refresh here would unnecessarily revisit message facts and hours.
func refreshAnalyticsSessionMetadata(ctx context.Context, tx *sql.Tx, key Key) error {
	_, err := tx.ExecContext(ctx, `UPDATE analytics_session SET
		(parent_id,directory,title,time_created,time_updated,project_id,source_deleted,is_subagent)=
		(SELECT parent_id,directory,title,created_at,updated_at,project_id,source_deleted,is_subagent
		 FROM session WHERE instance_id=? AND id=?) WHERE instance_id=? AND id=?`,
		key.InstanceID, key.SessionID, key.InstanceID, key.SessionID)
	return err
}
