package statev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// StreamUpdate applies source-ordered events for several sessions together
// with the stream cursor that produced them. A crash can therefore never
// advance the cursor past one session's events while losing another's.
// Replaying the current cursor is a no-op; any other predecessor mismatch
// fails with ErrConflict without applying anything.
type StreamUpdate struct {
	InstanceID string
	Checkpoint CheckpointUpdate
	Batches    []StreamBatch
}

// StreamBatch is one session's incremental change. Unlike a snapshot Batch,
// absent records are untouched and removals are retained as source-deleted.
// The store keeps the position of known records and appends new ones in the
// order supplied, so events cannot reorder previously ingested history.
type StreamBatch struct {
	Key Key
	// Session replaces metadata when set. Without it the session must already
	// exist: the consumer reads missing metadata from the source, never a guess.
	Session         *Session
	Messages        []Message
	Parts           []Part
	RemovedMessages []string
	RemovedParts    []PartRef
}

type PartRef struct{ MessageID, ID string }

type StreamResult struct {
	Duplicate bool
	Sessions  map[Key]int64 // committed revision per changed session
}

func (b StreamBatch) key() Key {
	if b.Session != nil {
		return b.Session.Key
	}
	return b.Key
}

func (d *DB) ApplyStream(ctx context.Context, u StreamUpdate) (StreamResult, error) {
	result := StreamResult{Sessions: map[Key]int64{}}
	c := u.Checkpoint
	if u.InstanceID == "" || c.Stream == "" || c.Cursor == "" || c.Cursor == c.ExpectedCursor {
		return result, fmt.Errorf("statev2: stream update must advance a named stream")
	}
	for _, b := range u.Batches {
		if k := b.key(); k.InstanceID != u.InstanceID || k.SessionID == "" {
			return result, fmt.Errorf("statev2: stream batch identity must belong to its instance")
		}
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	cursor, err := checkpoint(ctx, tx, u.InstanceID, c.Stream)
	if err != nil {
		return result, err
	}
	if cursor == c.Cursor {
		return StreamResult{Duplicate: true}, nil
	}
	if cursor != c.ExpectedCursor {
		return result, ErrConflict
	}
	for _, b := range u.Batches {
		revision, err := applyStreamBatch(ctx, tx, b)
		if err != nil {
			return StreamResult{}, err
		}
		result.Sessions[b.key()] = revision
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoint(instance_id,stream,cursor) VALUES(?,?,?)
		ON CONFLICT(instance_id,stream) DO UPDATE SET cursor=excluded.cursor`, u.InstanceID, c.Stream, c.Cursor); err != nil {
		return StreamResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return StreamResult{}, err
	}
	for key, revision := range result.Sessions {
		d.notifyCommitted(key, revision)
	}
	return result, nil
}

func applyStreamBatch(ctx context.Context, tx *sql.Tx, b StreamBatch) (int64, error) {
	key := b.key()
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM session WHERE instance_id=? AND id=?`, key.InstanceID, key.SessionID).Scan(&revision)
	switch {
	case errors.Is(err, sql.ErrNoRows) && b.Session == nil:
		return 0, fmt.Errorf("%w: stream event for session %s without captured metadata", ErrNotFound, key.SessionID)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	revision++
	// A full-snapshot digest describes the old revision, not this mutation.
	// Invalidate in the same transaction so a later identical snapshot repairs it.
	if _, err := tx.ExecContext(ctx, `DELETE FROM checkpoint WHERE instance_id=? AND stream=?`,
		key.InstanceID, "history/snapshot-v1/"+key.SessionID); err != nil {
		return 0, err
	}
	if b.Session != nil {
		s := *b.Session
		if _, err := tx.ExecContext(ctx, `INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,id) DO UPDATE SET
			title=excluded.title,directory=excluded.directory,created_at=excluded.created_at,updated_at=excluded.updated_at,
			source_deleted=excluded.source_deleted,revision=excluded.revision,extensions=excluded.extensions,
			parent_id=excluded.parent_id,project_id=excluded.project_id,read_format=excluded.read_format,is_subagent=excluded.is_subagent`,
			s.InstanceID, s.SessionID, s.Title, s.Directory, s.CreatedAt, s.UpdatedAt, s.SourceDeleted, revision, jsonValue(s.Extensions),
			s.ParentID, s.ProjectID, s.ReadFormat, s.Subagent); err != nil {
			return 0, err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE session SET revision=? WHERE instance_id=? AND id=?`, revision, key.InstanceID, key.SessionID); err != nil {
		return 0, err
	}
	batch := Batch{Session: Session{Key: key}, Messages: append([]Message(nil), b.Messages...), Parts: append([]Part(nil), b.Parts...)}
	if err := assignStreamPositions(ctx, tx, key, &batch); err != nil {
		return 0, err
	}
	if err := applyContent(ctx, tx, batch); err != nil {
		return 0, err
	}
	if err := projectAnalytics(ctx, tx, batch); err != nil {
		return 0, err
	}
	if err := retainRemovals(ctx, tx, key, b); err != nil {
		return 0, err
	}
	if err := refreshSidebar(ctx, tx, key); err != nil {
		return 0, err
	}
	if err := refreshAnalyticsSummary(ctx, tx, key, changedAnalyticsMessages(b)); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO change_outbox(instance_id,session_id,revision,kind) VALUES(?,?,?,'updated')`,
		key.InstanceID, key.SessionID, revision)
	return revision, err
}

// assignStreamPositions keeps a known record's position and appends new ones
// after the session's (or message's) current maximum, in supplied order.
func assignStreamPositions(ctx context.Context, tx *sql.Tx, key Key, b *Batch) error {
	next := int64(-1)
	for i := range b.Messages {
		m := &b.Messages[i]
		err := tx.QueryRowContext(ctx, `SELECT position FROM message WHERE instance_id=? AND session_id=? AND id=?`,
			key.InstanceID, key.SessionID, m.ID).Scan(&m.Position)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if next < 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(position)+1,0) FROM message WHERE instance_id=? AND session_id=?`,
				key.InstanceID, key.SessionID).Scan(&next); err != nil {
				return err
			}
		}
		m.Position = next
		next++
	}
	parts := map[string]int64{}
	for i := range b.Parts {
		p := &b.Parts[i]
		err := tx.QueryRowContext(ctx, `SELECT position FROM part WHERE instance_id=? AND session_id=? AND message_id=? AND id=?`,
			key.InstanceID, key.SessionID, p.MessageID, p.ID).Scan(&p.Position)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, ok := parts[p.MessageID]; !ok {
			var start int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(position)+1,0) FROM part WHERE instance_id=? AND session_id=? AND message_id=?`,
				key.InstanceID, key.SessionID, p.MessageID).Scan(&start); err != nil {
				return err
			}
			parts[p.MessageID] = start
		}
		p.Position = parts[p.MessageID]
		parts[p.MessageID]++
	}
	return nil
}

func retainRemovals(ctx context.Context, tx *sql.Tx, key Key, b StreamBatch) error {
	for _, id := range b.RemovedMessages {
		for _, stmt := range []string{
			`UPDATE message SET source_deleted=1 WHERE instance_id=? AND session_id=? AND id=?`,
			`UPDATE part SET source_deleted=1 WHERE instance_id=? AND session_id=? AND message_id=?`,
			`DELETE FROM analytics_message WHERE instance_id=? AND session_id=? AND id=?`,
			`DELETE FROM analytics_part WHERE instance_id=? AND session_id=? AND message_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, stmt, key.InstanceID, key.SessionID, id); err != nil {
				return err
			}
		}
	}
	for _, p := range b.RemovedParts {
		for _, stmt := range []string{
			`UPDATE part SET source_deleted=1 WHERE instance_id=? AND session_id=? AND message_id=? AND id=?`,
			`DELETE FROM analytics_part WHERE instance_id=? AND session_id=? AND message_id=? AND id=?`,
		} {
			if _, err := tx.ExecContext(ctx, stmt, key.InstanceID, key.SessionID, p.MessageID, p.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
