package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Commit durably applies one normalized batch. Acknowledgment and publication
// must happen only after it succeeds. It never calls platforms or lifecycle sinks.
func (d *DB) Commit(ctx context.Context, batch Batch) (CommitResult, error) {
	var result CommitResult
	if err := validateBatch(batch); err != nil {
		return result, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	s := batch.Session
	if c := batch.Checkpoint; c != nil {
		cursor, err := checkpoint(ctx, tx, s.InstanceID, c.Stream)
		if err != nil {
			return result, err
		}
		if cursor == c.Cursor {
			return CommitResult{Duplicate: true}, nil
		}
		if cursor != c.ExpectedCursor {
			return result, ErrConflict
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM purged_session WHERE instance_id=? AND session_id=?)`,
		s.InstanceID, s.SessionID).Scan(&result.Suppressed); err != nil {
		return result, err
	}
	if !result.Suppressed {
		if err := applySession(ctx, tx, batch, &result); err != nil {
			return CommitResult{}, err
		}
		if batch.Complete {
			if err := markSnapshotContent(ctx, tx, s.Key); err != nil {
				return CommitResult{}, err
			}
		}
		if err := applyContent(ctx, tx, batch); err != nil {
			return CommitResult{}, err
		}
		if err := projectAnalytics(ctx, tx, batch); err != nil {
			return CommitResult{}, err
		}
		if err := rebuildAnalyticsSummary(ctx, tx, s.Key); err != nil {
			return CommitResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO change_outbox(instance_id,session_id,revision,kind) VALUES(?,?,?,'updated')`,
			s.InstanceID, s.SessionID, result.Revision); err != nil {
			return CommitResult{}, err
		}
	}
	if c := batch.Checkpoint; c != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoint(instance_id,stream,cursor) VALUES(?,?,?)
			ON CONFLICT(instance_id,stream) DO UPDATE SET cursor=excluded.cursor`, s.InstanceID, c.Stream, c.Cursor); err != nil {
			return CommitResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return CommitResult{}, err
	}
	return result, nil
}

func markSnapshotContent(ctx context.Context, tx *sql.Tx, key Key) error {
	for _, table := range []string{"message", "part"} {
		if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET source_deleted=1 WHERE instance_id=? AND session_id=?",
			key.InstanceID, key.SessionID); err != nil {
			return err
		}
	}
	return nil
}

func applySession(ctx context.Context, tx *sql.Tx, batch Batch, result *CommitResult) error {
	s := batch.Session
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM session WHERE instance_id=? AND id=?`, s.InstanceID, s.SessionID).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if revision != batch.ExpectedRevision {
		return ErrConflict
	}
	result.Revision = revision + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,id) DO UPDATE SET
		title=excluded.title,directory=excluded.directory,created_at=excluded.created_at,updated_at=excluded.updated_at,
		source_deleted=excluded.source_deleted,revision=excluded.revision,extensions=excluded.extensions,
		parent_id=excluded.parent_id,project_id=excluded.project_id,read_format=excluded.read_format,is_subagent=excluded.is_subagent`,
		s.InstanceID, s.SessionID, s.Title, s.Directory, s.CreatedAt, s.UpdatedAt, s.SourceDeleted, result.Revision, jsonValue(s.Extensions),
		s.ParentID, s.ProjectID, s.ReadFormat, s.Subagent)
	return err
}

func applyContent(ctx context.Context, tx *sql.Tx, batch Batch) error {
	s := batch.Session
	for _, m := range batch.Messages {
		_, err := tx.ExecContext(ctx, `INSERT INTO message(instance_id,session_id,id,position,role,model,created_at,completed_at,source_deleted,extensions,
			provider,cost,tokens_input,tokens_output,tokens_reasoning,tokens_cache_read,tokens_cache_write,finish,has_error,error_name,agent,conversation_visible)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,session_id,id) DO UPDATE SET
			position=excluded.position,role=excluded.role,model=excluded.model,created_at=excluded.created_at,
			completed_at=excluded.completed_at,source_deleted=excluded.source_deleted,extensions=excluded.extensions,
			provider=excluded.provider,cost=excluded.cost,tokens_input=excluded.tokens_input,tokens_output=excluded.tokens_output,
			tokens_reasoning=excluded.tokens_reasoning,tokens_cache_read=excluded.tokens_cache_read,
			tokens_cache_write=excluded.tokens_cache_write,finish=excluded.finish,has_error=excluded.has_error,error_name=excluded.error_name,agent=excluded.agent,conversation_visible=excluded.conversation_visible`,
			s.InstanceID, s.SessionID, m.ID, m.Position, m.Role, m.Model, m.CreatedAt, m.CompletedAt, m.SourceDeleted, jsonValue(m.Extensions),
			m.Provider, m.Cost, m.TokensInput, m.TokensOutput, m.TokensReasoning, m.TokensCacheRead, m.TokensCacheWrite, m.Finish, m.HasError, m.ErrorName, m.Agent, m.ConversationVisible)
		if err != nil {
			return err
		}
	}
	for _, p := range batch.Parts {
		_, err := tx.ExecContext(ctx, `INSERT INTO part(instance_id,session_id,message_id,id,position,kind,text,source_deleted,extensions,created_at,text_present)
			VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,session_id,message_id,id) DO UPDATE SET
			position=excluded.position,kind=excluded.kind,text=excluded.text,
			source_deleted=excluded.source_deleted,extensions=excluded.extensions,created_at=excluded.created_at,text_present=excluded.text_present`,
			s.InstanceID, s.SessionID, p.MessageID, p.ID, p.Position, p.Kind, p.Text, p.SourceDeleted, jsonValue(p.Extensions), p.CreatedAt, p.TextPresent)
		if err != nil {
			return err
		}
	}
	return nil
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkpoint(ctx context.Context, q rowQuerier, instanceID, stream string) (string, error) {
	var cursor string
	err := q.QueryRowContext(ctx, `SELECT cursor FROM checkpoint WHERE instance_id=? AND stream=?`, instanceID, stream).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return cursor, err
}

func (d *DB) Checkpoint(ctx context.Context, instanceID, stream string) (string, error) {
	return checkpoint(ctx, d.db, instanceID, stream)
}

func jsonValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func validateBatch(b Batch) error {
	if b.Session.InstanceID == "" || b.Session.SessionID == "" || b.ExpectedRevision < 0 {
		return fmt.Errorf("statev2: invalid session identity or revision")
	}
	if c := b.Checkpoint; c != nil && (c.Stream == "" || c.Cursor == "" || c.Cursor == c.ExpectedCursor) {
		return fmt.Errorf("statev2: checkpoint must advance a named stream")
	}
	// Content constraints, JSON validity and parent identities are checked by
	// SQLite inside the same transaction as the checkpoint and outbox.
	return nil
}
