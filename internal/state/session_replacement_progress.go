package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrReplacementReconciliationPending = errors.New("replacement reconciliation has pending work")

type ReplacementCandidate struct {
	SessionID string               `json:"sessionId"`
	Directory string               `json:"directory"`
	Member    *bool                `json:"-"`
	Prior     *SessionInterruption `json:"prior,omitempty"`
}

type ReplacementReconciliation struct {
	Initialized, Seeded               bool
	StopStartedAt, AdmissionStartedAt int64
}

type ReplacementStopSnapshot struct {
	Baseline                      map[string]SessionInterruption
	StartedAt, AdmissionStartedAt int64
	Runtime                       ReplacementRuntime
	Membership                    map[string]bool
	Candidates                    []ReplacementCandidate
}

func migrateToV119(tx *sql.Tx) error {
	if err := addColumnIfMissing(tx, "session_replacement", "admission_started_at", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(tx, "session_replacement", "reconciliation_initialized", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_replacement_directory (
		platform TEXT NOT NULL, replacement_root TEXT NOT NULL, directory TEXT NOT NULL, member INTEGER NOT NULL,
		PRIMARY KEY(platform,replacement_root,directory));
		CREATE TABLE IF NOT EXISTS session_replacement_candidate (
		platform TEXT NOT NULL, replacement_root TEXT NOT NULL, session_id TEXT NOT NULL, directory TEXT NOT NULL,
		processed INTEGER NOT NULL DEFAULT 0, prior_json TEXT NOT NULL DEFAULT '', message_id TEXT NOT NULL DEFAULT '', observed_at INTEGER NOT NULL DEFAULT 0, message TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(platform,replacement_root,session_id));
		CREATE INDEX IF NOT EXISTS session_replacement_pending ON session_replacement_candidate(platform,replacement_root,processed,session_id)`)
	return err
}

func clearReplacementWork(ctx context.Context, tx *sql.Tx, platform, root string) error {
	for _, table := range []string{"session_replacement_candidate", "session_replacement_directory"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE platform=? AND replacement_root=?`, platform, root); err != nil {
			return err
		}
	}
	return nil
}

func saveReplacementMembership(ctx context.Context, tx *sql.Tx, platform, root string, members map[string]bool) error {
	data, err := json.Marshal(members)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO session_replacement_directory(platform,replacement_root,directory,member)
		SELECT ?,?,key,value FROM json_each(?)`, platform, root, string(data))
	return err
}

func saveReplacementCandidates(ctx context.Context, tx *sql.Tx, platform, root string, candidates []ReplacementCandidate) error {
	data, err := json.Marshal(candidates)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_replacement_candidate(platform,replacement_root,session_id,directory,prior_json)
		SELECT ?,?,json_extract(value,'$.sessionId'),json_extract(value,'$.directory'),COALESCE(json_extract(value,'$.prior'),'') FROM json_each(?)
		WHERE NOT EXISTS(SELECT 1 FROM session_replacement_directory d WHERE d.platform=? AND d.replacement_root=? AND d.directory=json_extract(value,'$.directory') AND d.member=0)`, platform, root, string(data), platform, root)
	return err
}

func (d *DB) ReplacementReconciliationStatus(ctx context.Context, platform, root string) (ReplacementReconciliation, error) {
	var status ReplacementReconciliation
	err := d.db.QueryRowContext(ctx, `SELECT reconciliation_initialized,stop_started_at,admission_started_at,
		EXISTS(SELECT 1 FROM session_replacement_candidate c WHERE c.platform=r.platform AND c.replacement_root=r.replacement_root)
		FROM session_replacement r WHERE platform=? AND replacement_root=?`, platform, root).Scan(&status.Initialized, &status.StopStartedAt, &status.AdmissionStartedAt, &status.Seeded)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	return status, err
}

func (d *DB) InitializeReplacementReconciliation(ctx context.Context, platform, root string, candidates []ReplacementCandidate) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE session_replacement SET reconciliation_initialized=1 WHERE platform=? AND replacement_root=? AND phase='stopped' AND reconciliation_initialized=0`, platform, root)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if err := saveReplacementCandidates(ctx, tx, platform, root, candidates); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ReplacementReconciliationBatch(ctx context.Context, platform, root string, limit int) ([]ReplacementCandidate, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT c.session_id,c.directory,d.member,c.prior_json FROM session_replacement_candidate c
		LEFT JOIN session_replacement_directory d ON d.platform=c.platform AND d.replacement_root=c.replacement_root AND d.directory=c.directory
		WHERE c.platform=? AND c.replacement_root=? AND c.processed=0 ORDER BY c.session_id LIMIT ?`, platform, root, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []ReplacementCandidate
	for rows.Next() {
		var candidate ReplacementCandidate
		var member sql.NullBool
		var prior string
		if err := rows.Scan(&candidate.SessionID, &candidate.Directory, &member, &prior); err != nil {
			return nil, err
		}
		if member.Valid {
			candidate.Member = &member.Bool
		}
		if prior != "" {
			candidate.Prior = &SessionInterruption{}
			if err := json.Unmarshal([]byte(prior), candidate.Prior); err != nil {
				return nil, err
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (d *DB) CheckpointReplacementCandidate(ctx context.Context, platform, root string, candidate ReplacementCandidate, member bool, notice SessionInterruption) error {
	return d.CheckpointReplacementBatch(ctx, platform, root, []ReplacementCheckpoint{{Candidate: candidate, Member: member, Notice: notice}})
}

type ReplacementCheckpoint struct {
	Candidate ReplacementCandidate
	Member    bool
	Notice    SessionInterruption
}

// One durable commit per read batch avoids a disk sync per historical session.
func (d *DB) CheckpointReplacementBatch(ctx context.Context, platform, root string, checkpoints []ReplacementCheckpoint) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, checkpoint := range checkpoints {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_replacement_directory(platform,replacement_root,directory,member) VALUES(?,?,?,?)`, platform, root, checkpoint.Candidate.Directory, checkpoint.Member); err != nil {
			return err
		}
		notice := checkpoint.Notice
		if _, err := tx.ExecContext(ctx, `UPDATE session_replacement_candidate SET processed=1,message_id=?,observed_at=?,message=? WHERE platform=? AND replacement_root=? AND session_id=?`, notice.MessageID, notice.ObservedAt, notice.Message, platform, root, checkpoint.Candidate.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Membership can complete before a subsequent lifecycle read times out. Keep
// that independent result so the next callback has its full budget for the read.
func (d *DB) CheckpointReplacementMembership(ctx context.Context, platform, root, directory string, member bool) error {
	_, err := d.db.ExecContext(ctx, `INSERT OR IGNORE INTO session_replacement_directory(platform,replacement_root,directory,member) VALUES(?,?,?,?)`, platform, root, directory, member)
	return err
}

// One SQL insert publishes the completed queue atomically, without another
// per-session loop or exposing partial reconciliation as confirmed history.
func (d *DB) PublishReplacementReconciliation(ctx context.Context, platform, root string) ([]string, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_replacement WHERE platform=? AND replacement_root=? AND phase='stopped' AND reconciliation_initialized=1)`, platform, root).Scan(&ready); err != nil {
		return nil, err
	}
	if !ready {
		return nil, ErrReplacementReconciliationPending
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_replacement_candidate WHERE platform=? AND replacement_root=? AND processed=0)`, platform, root).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, ErrReplacementReconciliationPending
	}
	rows, err := tx.QueryContext(ctx, `INSERT INTO session_interruption(platform,session_id,message_id,observed_at,message,replacement_root,confirmed)
		SELECT platform,session_id,message_id,observed_at,message,replacement_root,1 FROM session_replacement_candidate
		WHERE platform=? AND replacement_root=? AND message_id!=''
		ON CONFLICT(platform,session_id,message_id) DO UPDATE SET observed_at=excluded.observed_at,message=excluded.message,replacement_root=excluded.replacement_root,confirmed=1
		WHERE session_interruption.confirmed=0 RETURNING session_id`, platform, root)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := retireReplacementRuntime(ctx, tx, platform, root); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_replacement SET phase='confirmed',baseline_json='{}',stop_baseline_json='{}',stop_started_at=0,stop_runtime_json='',admission_started_at=0,reconciliation_initialized=0 WHERE platform=? AND replacement_root=?`, platform, root); err != nil {
		return nil, err
	}
	if err := clearReplacementWork(ctx, tx, platform, root); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
