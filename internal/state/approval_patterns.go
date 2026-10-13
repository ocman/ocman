package state

import (
	"context"
	"fmt"
	"time"
)

// settingExactApprovalPatterns marks Claude Code approval rows as holding
// exact (escaped) patterns.
const settingExactApprovalPatterns = "claudecode.exact_approval_patterns"

// EscapeLegacyApprovalPatterns rewrites, once, the patterns of Claude Code
// approvals recorded before prompt patterns were exact: each was the raw
// tool argument (`echo *` approved that command), and inheritance would
// otherwise read it as a glob. escape is providerprotocol.ExactPattern. A
// row an earlier build already escaped only narrows further. Local and
// remote (`r-<id>:claudecode`) rows are both rewritten; OpenCode rows keep
// OpenCode's glob patterns. Rows and marker commit together.
func (d *DB) EscapeLegacyApprovalPatterns(ctx context.Context, escape func(string) string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	var done int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM setting WHERE key = ?`, settingExactApprovalPatterns).Scan(&done); err != nil || done > 0 {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT rowid, patterns_json FROM auto_approved_permission
		WHERE platform = 'claudecode' OR platform LIKE '%:claudecode'`)
	if err != nil {
		return err
	}
	updates := map[int64]string{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		patterns, err := decodePatterns(raw)
		if err != nil {
			continue // unreadable patterns already inherit nothing
		}
		for i, p := range patterns {
			patterns[i] = escape(p)
		}
		if updates[id], err = encodePatterns(patterns); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, patterns := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE auto_approved_permission SET patterns_json = ? WHERE rowid = ?`, patterns, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO setting (key, value, updated_at) VALUES (?, '1', ?)`,
		settingExactApprovalPatterns, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("recording exact approval patterns: %w", err)
	}
	return tx.Commit()
}
