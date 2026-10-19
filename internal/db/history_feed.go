package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrHistoryUnsupported means this OpenCode schema has no durable per-session
// event sequence, so change detection must use a full snapshot comparison.
var ErrHistoryUnsupported = errors.New("opencode history feed requires the v2 schema")

// unsupported returns a schema check failure, or ErrHistoryUnsupported when
// the physical database does not use the v2 schema.
func unsupported(err error) error {
	if err != nil {
		return err
	}
	return ErrHistoryUnsupported
}

// HistorySequences returns each session's latest durable event sequence. It
// reads only OpenCode's indexed sequence table, never conversation content, so
// a restart can find exactly the sessions that changed while ocman was away.
func (d *DB) HistorySequences(ctx context.Context) (map[string]int64, error) {
	if v2, err := d.historyV2(ctx); err != nil || !v2 {
		return nil, unsupported(err)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT aggregate_id,seq FROM main.event_sequence WHERE aggregate_id LIKE 'ses%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, err
		}
		out[id] = seq
	}
	return out, rows.Err()
}

// SequenceVersion is the stored-history freshness version for a session: its
// latest durable event sequence, or "seq:none" when it has no events.
func SequenceVersion(seq int64, ok bool) string {
	if !ok {
		return "seq:none"
	}
	return fmt.Sprintf("seq:%d", seq)
}

// VersionCovers reports whether stored history validated at receipt reflects
// the source at head. Durable sequences ("seq:N") are ordered, so newer stored
// history also covers an older head; other versions must match exactly.
func VersionCovers(receipt, head string) bool {
	if receipt == head {
		return true
	}
	r, rok := parseSequence(receipt)
	if head == SequenceVersion(0, false) {
		return rok
	}
	h, hok := parseSequence(head)
	return rok && hok && r >= h
}

func parseSequence(version string) (int64, bool) {
	raw, ok := strings.CutPrefix(version, "seq:")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil
}

// HistorySequence reads one session's latest durable sequence version.
func (d *DB) HistorySequence(ctx context.Context, sessionID string) (string, error) {
	if v2, err := d.historyV2(ctx); err != nil || !v2 {
		return "", unsupported(err)
	}
	var seq int64
	err := d.db.QueryRowContext(ctx, `SELECT seq FROM main.event_sequence WHERE aggregate_id=?`, sessionID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return SequenceVersion(0, false), nil
	}
	return SequenceVersion(seq, true), err
}

// HistoryMessages reads session metadata and only the named messages in one
// read transaction, converted exactly like HistorySnapshot. Unknown IDs are
// omitted. An empty list reads metadata only.
func (d *DB) HistoryMessages(ctx context.Context, sessionID string, messageIDs []string) (*HistorySnapshot, error) {
	if v2, err := d.historyV2(ctx); err != nil || !v2 {
		return nil, unsupported(err)
	}
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := &HistorySnapshot{Messages: []Message{}, Parts: []Part{}}
	s := &out.Session
	if err := tx.QueryRowContext(ctx, `SELECT id,project_id,COALESCE(parent_id,''),title,directory,time_created,time_updated,
		summary_additions,summary_deletions,summary_files,share_url FROM session WHERE id=?`, sessionID).
		Scan(&s.ID, &s.ProjectID, &s.ParentID, &s.Title, &s.Directory, &s.TimeCreated, &s.TimeUpdated,
			&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles, &s.ShareURL); err != nil {
		return nil, err
	}
	out.Version = fmt.Sprint(s.TimeUpdated)
	out.Subagent = IsSubagentTitle(s.Title)
	if messageIDs == nil {
		messageIDs = []string{}
	}
	return readV2History(ctx, tx, out, messageIDs...)
}
