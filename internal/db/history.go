package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// HistoryEntry.Version is an opaque change hint, not a cursor or timestamp
// ordering guarantee. Periodic verification rereads old rows to measure
// content that changed without a new hint.
type HistoryEntry struct {
	ID      string
	Version string
}

type HistorySnapshot struct {
	// InferredStatus is static transcript evidence, never a live running signal.
	InferredStatus SessionStatus
	// Partial snapshots may add metadata/content but never authorize removals.
	Partial bool
	// MessageOffset is the absolute position of the first message in a tail read.
	MessageOffset int64
	// SourceCursor is opaque resumable source progress, committed with the content.
	SourceCursor string
	// SourceChild distinguishes native descendants from application child links.
	SourceChild      *bool
	ProjectDirectory string
	Session          Session
	Messages         []Message
	Parts            []Part
	Version          string
	NativeMessages   map[string]json.RawMessage // original v2 payloads, including unsupported native message types
	// Subagent marks a helper session the agent spawned. Each adapter
	// decides it by its own convention; analytics leaves it out of session
	// and prompt counts.
	Subagent bool
}

// IsSubagentTitle reports OpenCode's "(<name> subagent)" title convention,
// the Go form of the analytics filter title LIKE '%(% subagent)'.
func IsSubagentTitle(title string) bool {
	const suffix = " subagent)"
	if len(title) < len(suffix) {
		return false
	}
	head, tail := title[:len(title)-len(suffix)], title[len(title)-len(suffix):]
	return strings.EqualFold(tail, suffix) && strings.Contains(head, "(")
}

var ErrHistoryChanged = errors.New("history changed during snapshot; retry")

// HistorySourceKey identifies the physical database, not an analytics mirror.
func (d *DB) HistorySourceKey(ctx context.Context) (string, error) {
	var path string
	err := d.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path)
	if err == nil && path == "" {
		err = fmt.Errorf("history requires a file-backed source")
	}
	return path, err
}

func (d *DB) HistoryInventory(ctx context.Context) ([]HistoryEntry, error) {
	// Newest first, so the sessions a user is most likely to resume import first.
	rows, err := d.db.QueryContext(ctx, `SELECT id, CAST(time_updated AS TEXT) FROM session ORDER BY time_updated DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.Version); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// HistorySnapshot reads the source metadata and complete content in one SQLite
// transaction. V2 uses the existing connection-local views and ConvertMessage;
// no second native-to-v1 converter or lossy summary stripping is introduced.
func (d *DB) HistorySnapshot(ctx context.Context, id string) (*HistorySnapshot, error) {
	v2, err := d.historyV2(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := &HistorySnapshot{Messages: []Message{}, Parts: []Part{}}
	s := &out.Session
	err = tx.QueryRowContext(ctx, `SELECT id,project_id,COALESCE(parent_id,''),title,directory,time_created,time_updated,
		summary_additions,summary_deletions,summary_files,share_url FROM session WHERE id=?`, id).
		Scan(&s.ID, &s.ProjectID, &s.ParentID, &s.Title, &s.Directory, &s.TimeCreated, &s.TimeUpdated,
			&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles, &s.ShareURL)
	if err != nil {
		return nil, err
	}
	out.Version = fmt.Sprint(s.TimeUpdated)
	out.Subagent = IsSubagentTitle(s.Title)
	if v2 {
		return readV2History(ctx, tx, out)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,session_id,time_created,data FROM message
		WHERE session_id=? ORDER BY time_created,id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m Message
		var raw string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.TimeCreated, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		m.Data = []byte(raw)
		out.Messages = append(out.Messages, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,message_id,session_id,time_created,data FROM part
		WHERE session_id=? ORDER BY time_created,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Part
		var raw string
		if err := rows.Scan(&p.ID, &p.MessageID, &p.SessionID, &p.TimeCreated, &raw); err != nil {
			return nil, err
		}
		p.Data = []byte(raw)
		out.Parts = append(out.Parts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
