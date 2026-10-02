package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const routineRunColumns = `id, routine_id, routine_updated_at, routine_name, prompt, directory, remote_id, agent, model, session_mode, target_session_id, trigger,
	platform, session_id, state, error, occurrence_at, created_at, started_at, finished_at, archive_session_after_success, notify_on_success`

func scanRoutineRun(row routineScanner) (RoutineRun, error) {
	var run RoutineRun
	err := row.Scan(&run.ID, &run.RoutineID, &run.RoutineUpdatedAt, &run.RoutineName, &run.Prompt, &run.Directory, &run.RemoteID,
		&run.Agent, &run.Model, &run.SessionMode, &run.TargetSessionID, &run.Trigger, &run.Platform, &run.SessionID, &run.State, &run.Error, &run.OccurrenceAt,
		&run.CreatedAt, &run.StartedAt, &run.FinishedAt, &run.ArchiveSessionAfterSuccess, &run.NotifyOnSuccess)
	return run, err
}

// ClaimRoutineRun creates the occurrence and its definition snapshot together.
// A competing claimant for the same routine occurrence receives claimed=false.
func (d *DB) ClaimRoutineRun(ctx context.Context, run RoutineRun) (RoutineRun, bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return RoutineRun{}, false, fmt.Errorf("beginning routine run claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	routine, err := scanRoutine(tx.QueryRowContext(ctx, `SELECT `+routineColumns+` FROM routine WHERE id = ? AND deleted = 0`, run.RoutineID))
	if errors.Is(err, sql.ErrNoRows) {
		return RoutineRun{}, false, ErrRoutineNotFound
	}
	if err != nil {
		return RoutineRun{}, false, fmt.Errorf("reading routine for claim: %w", err)
	}
	run.RoutineUpdatedAt, run.RoutineName, run.Prompt, run.Directory, run.RemoteID, run.Agent, run.Model, run.SessionMode, run.TargetSessionID = routine.UpdatedAt, routine.Name, routine.Prompt, routine.Directory, routine.RemoteID, routine.Agent, routine.Model, routine.SessionMode, routine.SessionID
	run.ArchiveSessionAfterSuccess, run.NotifyOnSuccess = routine.ArchiveSessionAfterSuccess, routine.NotifyOnSuccess
	result, err := tx.ExecContext(ctx, `INSERT INTO routine_run (`+routineRunColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, run.ID, run.RoutineID, run.RoutineUpdatedAt, run.RoutineName, run.Prompt,
		run.Directory, run.RemoteID, run.Agent, run.Model, run.SessionMode, run.TargetSessionID, run.Trigger, run.Platform, run.SessionID, run.State, run.Error,
		run.OccurrenceAt, run.CreatedAt, run.StartedAt, run.FinishedAt, run.ArchiveSessionAfterSuccess, run.NotifyOnSuccess)
	if err != nil {
		return RoutineRun{}, false, fmt.Errorf("claiming routine run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return RoutineRun{}, false, err
	}
	if changed == 0 {
		run, err = scanRoutineRun(tx.QueryRowContext(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE routine_id = ? AND occurrence_at = ?`, run.RoutineID, run.OccurrenceAt))
		if errors.Is(err, sql.ErrNoRows) {
			return RoutineRun{}, false, ErrRoutineRunActive
		}
		return run, false, err
	}
	if err := tx.Commit(); err != nil {
		return RoutineRun{}, false, fmt.Errorf("committing routine run claim: %w", err)
	}
	return run, true, nil
}

func (d *DB) GetRoutineRun(ctx context.Context, id string) (RoutineRun, error) {
	run, err := scanRoutineRun(d.db.QueryRowContext(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RoutineRun{}, ErrRoutineRunNotFound
	}
	if err != nil {
		return RoutineRun{}, fmt.Errorf("getting routine run: %w", err)
	}
	return run, nil
}

func (d *DB) ListRoutineRuns(ctx context.Context, routineID string) ([]RoutineRun, error) {
	return d.queryRoutineRuns(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE routine_id = ? ORDER BY created_at DESC, id DESC`, routineID)
}

// RoutineRunCursor is the (created_at, id) of the oldest run already shown;
// the zero value starts at the newest run.
type RoutineRunCursor struct {
	CreatedAt int64
	ID        string
}

// ListRoutineRunsPage returns at most limit runs, newest first, strictly older
// than before. Keyset paging keeps pages stable while new runs are inserted.
func (d *DB) ListRoutineRunsPage(ctx context.Context, routineID string, limit int, before RoutineRunCursor) ([]RoutineRun, error) {
	if before.ID == "" {
		return d.queryRoutineRuns(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE routine_id = ?
			ORDER BY created_at DESC, id DESC LIMIT ?`, routineID, limit)
	}
	return d.queryRoutineRuns(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE routine_id = ? AND (created_at, id) < (?, ?)
		ORDER BY created_at DESC, id DESC LIMIT ?`, routineID, before.CreatedAt, before.ID, limit)
}

// LatestRoutineRuns maps each live routine to its newest run, one indexed
// lookup per routine (routine_run_history_idx).
func (d *DB) LatestRoutineRuns(ctx context.Context) (map[string]RoutineRun, error) {
	runs, err := d.queryRoutineRuns(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE id IN (
		SELECT (SELECT id FROM routine_run WHERE routine_id = routine.id ORDER BY created_at DESC, id DESC LIMIT 1)
		FROM routine WHERE deleted = 0)`)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]RoutineRun, len(runs))
	for _, run := range runs {
		latest[run.RoutineID] = run
	}
	return latest, nil
}

func (d *DB) queryRoutineRuns(ctx context.Context, query string, args ...any) ([]RoutineRun, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing routine runs: %w", err)
	}
	defer rows.Close()
	var runs []RoutineRun
	for rows.Next() {
		run, err := scanRoutineRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning routine run: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (d *DB) UpdateRoutineRun(ctx context.Context, run RoutineRun) error {
	result, err := d.db.ExecContext(ctx, `UPDATE routine_run SET platform = ?, session_id = ?, state = ?, error = ?,
		started_at = CASE WHEN ? = 0 THEN started_at ELSE ? END,
		finished_at = CASE WHEN ? = 0 THEN finished_at ELSE ? END WHERE id = ?`,
		run.Platform, run.SessionID, run.State, run.Error, run.StartedAt, run.StartedAt, run.FinishedAt, run.FinishedAt, run.ID)
	if err != nil {
		return fmt.Errorf("updating routine run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrRoutineRunNotFound
	}
	return nil
}

func (d *DB) ListRunningRoutineRuns(ctx context.Context) ([]RoutineRun, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE state = 'running' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("listing running routine runs: %w", err)
	}
	defer rows.Close()
	var runs []RoutineRun
	for rows.Next() {
		run, err := scanRoutineRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning running routine run: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// RoutineSessions maps each session a routine launched to its routine ID.
// Sessions a routine merely prompted (session mode "existing") belong to the
// user and are not tagged.
// ponytail: full scan of routine_run per session list; index (platform, session_id) if history grows large.
func (d *DB) RoutineSessions(ctx context.Context) (map[Key]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT platform, session_id, routine_id FROM routine_run
		WHERE session_id != '' AND session_mode != 'existing' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("listing routine sessions: %w", err)
	}
	defer rows.Close()
	sessions := make(map[Key]string)
	for rows.Next() {
		var key Key
		var routineID string
		if err := rows.Scan(&key.Platform, &key.SessionID, &routineID); err != nil {
			return nil, fmt.Errorf("scanning routine session: %w", err)
		}
		sessions[key] = routineID
	}
	return sessions, rows.Err()
}

func (d *DB) LinkRoutineRun(ctx context.Context, id, platform, sessionID string, startedAt int64, bindRoutine bool) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning routine run link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if bindRoutine {
		if _, err := tx.ExecContext(ctx, `UPDATE routine SET session_id = ? WHERE id = (
			SELECT routine_id FROM routine_run WHERE id = ? AND session_mode = 'reuse' AND target_session_id = ''
		) AND directory = (SELECT directory FROM routine_run WHERE id = ?) AND remote_id = (SELECT remote_id FROM routine_run WHERE id = ?)
		AND session_mode = 'reuse' AND session_id = ''`, sessionID, id, id, id); err != nil {
			return fmt.Errorf("binding routine session: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE routine_run SET platform = ?, session_id = ?, started_at = ? WHERE id = ? AND state = 'running'`, platform, sessionID, startedAt, id)
	if err != nil {
		return fmt.Errorf("linking routine run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrRoutineRunNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing routine run link: %w", err)
	}
	return nil
}

// FinishRoutineRun settles a claimed occurrence and advances its routine in
// one transaction. Successful delete-after-success routines are soft deleted.
// reply, the session's final assistant text, is only shown in the Inbox item,
// which is posted for a successful run only when the run's routine opted in.
//
// A consuming run advances the routine if it is unchanged since the claim or
// still due at this run's occurrence: an edit that kept the schedule (a
// prompt-only patch) must not strand the occurrence, while a schedule edit
// moves next_due_at and is left alone.
//
// advance is false for a run that did not consume a scheduled occurrence
// (manual, webhook). Such a run leaves next_due_at, enabled and updated_at
// alone, so it cannot invalidate the routine version an overlapping scheduled
// run captured and must match to consume its occurrence.
func (d *DB) FinishRoutineRun(ctx context.Context, id, runState, errorText, reply string, finishedAt, nextDueAt int64, enabled, advance bool) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("beginning routine run finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var routineID string
	var routineUpdatedAt, occurrenceAt int64
	if err := tx.QueryRowContext(ctx, `SELECT routine_id, routine_updated_at, occurrence_at FROM routine_run WHERE id = ? AND state = 'running'`, id).Scan(&routineID, &routineUpdatedAt, &occurrenceAt); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("reading routine run for finish: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE routine_run SET state = ?, error = ?, finished_at = ? WHERE id = ? AND state = 'running'`, runState, errorText, finishedAt, id); err != nil {
		return false, fmt.Errorf("finishing routine run: %w", err)
	}
	if !advance {
		if runState == "success" {
			if _, err := tx.ExecContext(ctx, `UPDATE routine SET next_due_at = 0, enabled = 0, deleted = 1, deleted_at = ?, updated_at = ?
				WHERE id = ? AND deleted = 0 AND delete_after_success = 1 AND updated_at = ?`, finishedAt, finishedAt, routineID, routineUpdatedAt); err != nil {
				return false, fmt.Errorf("deleting successful routine: %w", err)
			}
		}
	} else if runState == "success" {
		if _, err := tx.ExecContext(ctx, `UPDATE routine SET
			next_due_at = CASE WHEN delete_after_success = 1 THEN 0 ELSE ? END,
			enabled = CASE WHEN delete_after_success = 1 THEN 0 ELSE ? END,
			deleted = CASE WHEN delete_after_success = 1 THEN 1 ELSE deleted END,
			deleted_at = CASE WHEN delete_after_success = 1 THEN ? ELSE deleted_at END,
			updated_at = ? WHERE id = ? AND deleted = 0 AND (updated_at = ? OR next_due_at = ?)`, nextDueAt, enabled, finishedAt, finishedAt, routineID, routineUpdatedAt, occurrenceAt); err != nil {
			return false, fmt.Errorf("advancing successful routine: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE routine SET next_due_at = ?, enabled = ?, updated_at = ? WHERE id = ? AND deleted = 0 AND (updated_at = ? OR next_due_at = ?)`, nextDueAt, enabled, finishedAt, routineID, routineUpdatedAt, occurrenceAt); err != nil {
		return false, fmt.Errorf("advancing failed routine: %w", err)
	}
	body := fmt.Sprintf("Run %s finished with status **%s**.\n\n", id, runState)
	for _, section := range []string{errorText, reply} {
		if section = strings.TrimSpace(section); section != "" {
			body += section + "\n\n"
		}
	}
	body += "[View routines](/routines)"
	if _, err := tx.ExecContext(ctx, `INSERT INTO inbox_item (id, title, body, created_at, category, session_json)
		SELECT ?, routine_name || ': ' || ?, ?, ?, 'routine',
		CASE WHEN platform <> '' AND session_id <> '' THEN json_object('platform', platform, 'sessionId', session_id) ELSE '' END
		FROM routine_run WHERE id = ? AND (? <> 'success' OR notify_on_success = 1)`, "routine-"+id, runState, body, finishedAt, id, runState); err != nil {
		return false, fmt.Errorf("notifying routine completion: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing routine run finish: %w", err)
	}
	return true, nil
}
