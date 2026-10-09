package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrRoutineNotFound    = errors.New("routine not found")
	ErrRoutineRunNotFound = errors.New("routine run not found")
	ErrRoutineRunActive   = errors.New("routine already has an active shared-session run")
)

type Routine struct {
	ID                         string `json:"id"`
	Name                       string `json:"name"`
	Prompt                     string `json:"prompt"`
	Directory                  string `json:"directory"`
	RemoteID                   string `json:"remoteId"`
	Agent                      string `json:"agent"`
	Model                      string `json:"model"`
	SessionMode                string `json:"sessionMode"`
	SessionID                  string `json:"sessionId"`
	Worktree                   bool   `json:"worktree"`
	CleanupWorktree            bool   `json:"cleanupWorktree"`
	ScheduleKind               string `json:"scheduleKind"`
	ScheduleConfigJSON         string `json:"scheduleConfigJSON"`
	PermissionRulesJSON        string `json:"permissionRulesJSON"`
	NextDueAt                  int64  `json:"nextDueAt"`
	Enabled                    bool   `json:"enabled"`
	Deleted                    bool   `json:"deleted"`
	DeleteAfterSuccess         bool   `json:"deleteAfterSuccess"`
	ArchiveSessionAfterSuccess bool   `json:"archiveSessionAfterSuccess"`
	// NotifyOnSuccess also posts an Inbox item for a successful run; by
	// default only runs that don't succeed are reported.
	NotifyOnSuccess bool  `json:"notifyOnSuccess"`
	CreatedAt       int64 `json:"createdAt"`
	UpdatedAt       int64 `json:"updatedAt"`
	DeletedAt       int64 `json:"deletedAt,omitempty"`
	ExpiredAt       int64 `json:"expiredAt,omitempty"`
	// LatestRun is the newest run; only the REST list fills it.
	LatestRun *RoutineRun `json:"latestRun,omitempty"`
}

type RoutineRun struct {
	ID                         string `json:"id"`
	RoutineID                  string `json:"routineId"`
	RoutineUpdatedAt           int64  `json:"routineUpdatedAt"`
	RoutineName                string `json:"routineName"`
	Prompt                     string `json:"prompt"`
	Directory                  string `json:"directory"`
	RemoteID                   string `json:"remoteId"`
	Agent                      string `json:"agent"`
	Model                      string `json:"model"`
	SessionMode                string `json:"sessionMode"`
	TargetSessionID            string `json:"targetSessionId"`
	Worktree                   bool   `json:"worktree"`
	CleanupWorktree            bool   `json:"cleanupWorktree"`
	WorktreePath               string `json:"worktreePath,omitempty"`
	Trigger                    string `json:"trigger"`
	Platform                   string `json:"platform,omitempty"`
	SessionID                  string `json:"sessionId,omitempty"`
	State                      string `json:"state"`
	Error                      string `json:"error,omitempty"`
	OccurrenceAt               int64  `json:"occurrenceAt"`
	CreatedAt                  int64  `json:"createdAt"`
	StartedAt                  int64  `json:"startedAt,omitempty"`
	FinishedAt                 int64  `json:"finishedAt,omitempty"`
	ArchiveSessionAfterSuccess bool   `json:"archiveSessionAfterSuccess"`
	NotifyOnSuccess            bool   `json:"notifyOnSuccess"`
}

const routineColumns = `id, name, prompt, directory, remote_id, agent, model, session_mode, session_id, schedule_kind, schedule_config_json, permission_rules_json,
	next_due_at, enabled, deleted, delete_after_success, created_at, updated_at, deleted_at, expired_at, archive_session_after_success, notify_on_success, worktree, cleanup_worktree`

type routineScanner interface{ Scan(...any) error }

func scanRoutine(row routineScanner) (Routine, error) {
	var routine Routine
	err := row.Scan(&routine.ID, &routine.Name, &routine.Prompt, &routine.Directory, &routine.RemoteID,
		&routine.Agent, &routine.Model, &routine.SessionMode, &routine.SessionID, &routine.ScheduleKind, &routine.ScheduleConfigJSON, &routine.PermissionRulesJSON, &routine.NextDueAt, &routine.Enabled,
		&routine.Deleted, &routine.DeleteAfterSuccess, &routine.CreatedAt, &routine.UpdatedAt, &routine.DeletedAt, &routine.ExpiredAt, &routine.ArchiveSessionAfterSuccess, &routine.NotifyOnSuccess, &routine.Worktree, &routine.CleanupWorktree)
	return routine, err
}

func (d *DB) CreateRoutine(ctx context.Context, routine Routine) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO routine (`+routineColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		routine.ID, routine.Name, routine.Prompt, routine.Directory, routine.RemoteID, routine.Agent, routine.Model, routine.SessionMode, routine.SessionID, routine.ScheduleKind,
		routine.ScheduleConfigJSON, routine.PermissionRulesJSON, routine.NextDueAt, routine.Enabled, routine.Deleted, routine.DeleteAfterSuccess,
		routine.CreatedAt, routine.UpdatedAt, routine.DeletedAt, routine.ExpiredAt, routine.ArchiveSessionAfterSuccess, routine.NotifyOnSuccess, routine.Worktree, routine.CleanupWorktree)
	if err != nil {
		return fmt.Errorf("creating routine: %w", err)
	}
	return nil
}

func (d *DB) GetRoutine(ctx context.Context, id string) (Routine, error) {
	routine, err := scanRoutine(d.db.QueryRowContext(ctx, `SELECT `+routineColumns+` FROM routine WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Routine{}, ErrRoutineNotFound
	}
	if err != nil {
		return Routine{}, fmt.Errorf("getting routine: %w", err)
	}
	return routine, nil
}

func (d *DB) ListRoutines(ctx context.Context, includeDeleted bool) ([]Routine, error) {
	query := `SELECT ` + routineColumns + ` FROM routine`
	if !includeDeleted {
		query += ` WHERE deleted = 0`
	}
	query += ` ORDER BY deleted, name COLLATE NOCASE, id`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing routines: %w", err)
	}
	defer rows.Close()
	var routines []Routine
	for rows.Next() {
		routine, err := scanRoutine(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning routine: %w", err)
		}
		routines = append(routines, routine)
	}
	return routines, rows.Err()
}

func (d *DB) UpdateRoutine(ctx context.Context, routine Routine) error {
	result, err := d.db.ExecContext(ctx, `UPDATE routine SET name = ?, prompt = ?, directory = ?, remote_id = ?,
		agent = ?, model = ?, session_mode = ?, session_id = CASE
			WHEN ? = 'reuse' AND session_mode = 'reuse' AND directory = ? AND remote_id = ? THEN session_id ELSE ? END,
		schedule_kind = ?, schedule_config_json = ?, permission_rules_json = ?, next_due_at = ?, enabled = ?, delete_after_success = ?, archive_session_after_success = ?, notify_on_success = ?, worktree = ?, cleanup_worktree = ?, updated_at = ?, expired_at = 0
		WHERE id = ? AND deleted = 0`, routine.Name, routine.Prompt, routine.Directory, routine.RemoteID,
		routine.Agent, routine.Model, routine.SessionMode, routine.SessionMode, routine.Directory, routine.RemoteID, routine.SessionID, routine.ScheduleKind, routine.ScheduleConfigJSON, routine.PermissionRulesJSON, routine.NextDueAt, routine.Enabled, routine.DeleteAfterSuccess,
		routine.ArchiveSessionAfterSuccess, routine.NotifyOnSuccess, routine.Worktree, routine.CleanupWorktree, routine.UpdatedAt, routine.ID)
	if err != nil {
		return fmt.Errorf("updating routine: %w", err)
	}
	return requireRoutineChange(result)
}

func (d *DB) SoftDeleteRoutine(ctx context.Context, id string, now int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning routine deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE routine SET enabled = 0, deleted = 1, deleted_at = ?, updated_at = ? WHERE id = ? AND deleted = 0`, now, now, id)
	if err != nil {
		return fmt.Errorf("deleting routine: %w", err)
	}
	if err := requireRoutineChange(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE webhook_dispatch SET state='cancelled', finished_at=? WHERE routine_id=? AND state='queued'`, now, id); err != nil {
		return fmt.Errorf("cancelling webhook dispatches: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing routine deletion: %w", err)
	}
	return nil
}

func requireRoutineChange(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrRoutineNotFound
	}
	return nil
}

func (d *DB) ListDueRoutines(ctx context.Context, now int64) ([]Routine, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+routineColumns+` FROM routine
		WHERE enabled = 1 AND deleted = 0 AND next_due_at > 0 AND next_due_at <= ?
		AND NOT EXISTS (SELECT 1 FROM routine_run WHERE routine_id = routine.id AND state = 'running' AND session_mode <> 'new')
		ORDER BY next_due_at, id`, now)
	if err != nil {
		return nil, fmt.Errorf("listing due routines: %w", err)
	}
	defer rows.Close()
	var routines []Routine
	for rows.Next() {
		routine, err := scanRoutine(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning due routine: %w", err)
		}
		routines = append(routines, routine)
	}
	return routines, rows.Err()
}

func (d *DB) ExpireOverdueTimeoutRoutines(ctx context.Context, now int64) error {
	_, err := d.db.ExecContext(ctx, `UPDATE routine SET enabled = 0, expired_at = ?, updated_at = ?
		WHERE enabled = 1 AND deleted = 0 AND schedule_kind = 'timeout' AND next_due_at > 0 AND next_due_at <= ?
		AND NOT EXISTS (SELECT 1 FROM routine_run WHERE routine_id = routine.id AND occurrence_at = routine.next_due_at)`, now, now, now)
	if err != nil {
		return fmt.Errorf("expiring overdue timeout routines: %w", err)
	}
	return nil
}
