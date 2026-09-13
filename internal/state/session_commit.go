package state

import (
	"context"
	"fmt"
	"time"
)

// SessionCommit is one immutable commit summary observed in a live terminal
// tool result. Branch is nil when git printed a detached-HEAD summary.
type SessionCommit struct {
	Order           int64   `json:"order"`
	Platform        string  `json:"-"`
	SessionID       string  `json:"-"`
	SHA             string  `json:"sha"`
	Branch          *string `json:"branch"`
	Subject         string  `json:"subject"`
	SourceMessageID string  `json:"sourceMessageId"`
	ToolPartID      string  `json:"toolPartId"`
	ToolCallID      string  `json:"toolCallId"`
	SourceCallID    string  `json:"-"`
	ObservedAt      int64   `json:"observedAt"`
}

// RecordSessionCommit inserts an observation unless the same call already
// reported the same printed SHA. It returns true only for a new row.
func (d *DB) RecordSessionCommit(ctx context.Context, commit SessionCommit) (bool, error) {
	if commit.ObservedAt == 0 {
		commit.ObservedAt = time.Now().UnixMilli()
	}
	result, err := d.db.ExecContext(ctx, `
		INSERT INTO session_commit (
			platform, session_id, sha, branch, subject, source_message_id,
			tool_part_id, tool_call_id, source_call_id, observed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, session_id, source_call_id, sha) DO NOTHING`,
		commit.Platform, commit.SessionID, commit.SHA, commit.Branch, commit.Subject,
		commit.SourceMessageID, commit.ToolPartID, commit.ToolCallID, commit.SourceCallID, commit.ObservedAt)
	if err != nil {
		return false, fmt.Errorf("recording session commit: %w", err)
	}
	inserted, err := result.RowsAffected()
	return inserted > 0, err
}

func (d *DB) ListSessionCommits(ctx context.Context, platform, sessionID string) ([]SessionCommit, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT observation_order, sha, branch, subject, source_message_id,
		       tool_part_id, tool_call_id, source_call_id, observed_at
		FROM session_commit
		WHERE platform = ? AND session_id = ?
		ORDER BY observation_order`, platform, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing session commits: %w", err)
	}
	defer rows.Close()
	commits := []SessionCommit{}
	for rows.Next() {
		commit := SessionCommit{Platform: platform, SessionID: sessionID}
		if err := rows.Scan(&commit.Order, &commit.SHA, &commit.Branch, &commit.Subject,
			&commit.SourceMessageID, &commit.ToolPartID, &commit.ToolCallID,
			&commit.SourceCallID, &commit.ObservedAt); err != nil {
			return nil, fmt.Errorf("scanning session commit: %w", err)
		}
		commits = append(commits, commit)
	}
	return commits, rows.Err()
}
