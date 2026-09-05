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
)

type Routine struct {
	ID                 string
	Name               string
	Prompt             string
	Directory          string
	RemoteID           string
	ScheduleKind       string
	ScheduleConfigJSON string
	NextDueAt          int64
	Enabled            bool
	Deleted            bool
	DeleteAfterSuccess bool
	CreatedAt          int64
	UpdatedAt          int64
	DeletedAt          int64
}

type RoutineRun struct {
	ID           string
	RoutineID    string
	RoutineName  string
	Prompt       string
	Directory    string
	RemoteID     string
	Trigger      string
	Platform     string
	SessionID    string
	State        string
	Error        string
	OccurrenceAt int64
	CreatedAt    int64
	StartedAt    int64
	FinishedAt   int64
}

const routineColumns = `id, name, prompt, directory, remote_id, schedule_kind, schedule_config_json,
	next_due_at, enabled, deleted, delete_after_success, created_at, updated_at, deleted_at`

type routineScanner interface{ Scan(...any) error }

func scanRoutine(row routineScanner) (Routine, error) {
	var routine Routine
	err := row.Scan(&routine.ID, &routine.Name, &routine.Prompt, &routine.Directory, &routine.RemoteID,
		&routine.ScheduleKind, &routine.ScheduleConfigJSON, &routine.NextDueAt, &routine.Enabled,
		&routine.Deleted, &routine.DeleteAfterSuccess, &routine.CreatedAt, &routine.UpdatedAt, &routine.DeletedAt)
	return routine, err
}

func (d *DB) CreateRoutine(ctx context.Context, routine Routine) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO routine (`+routineColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		routine.ID, routine.Name, routine.Prompt, routine.Directory, routine.RemoteID, routine.ScheduleKind,
		routine.ScheduleConfigJSON, routine.NextDueAt, routine.Enabled, routine.Deleted, routine.DeleteAfterSuccess,
		routine.CreatedAt, routine.UpdatedAt, routine.DeletedAt)
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
		schedule_kind = ?, schedule_config_json = ?, next_due_at = ?, enabled = ?, delete_after_success = ?, updated_at = ?
		WHERE id = ? AND deleted = 0`, routine.Name, routine.Prompt, routine.Directory, routine.RemoteID,
		routine.ScheduleKind, routine.ScheduleConfigJSON, routine.NextDueAt, routine.Enabled, routine.DeleteAfterSuccess,
		routine.UpdatedAt, routine.ID)
	if err != nil {
		return fmt.Errorf("updating routine: %w", err)
	}
	return requireRoutineChange(result)
}

func (d *DB) SoftDeleteRoutine(ctx context.Context, id string, now int64) error {
	result, err := d.db.ExecContext(ctx, `UPDATE routine SET enabled = 0, deleted = 1, deleted_at = ?, updated_at = ? WHERE id = ? AND deleted = 0`, now, now, id)
	if err != nil {
		return fmt.Errorf("deleting routine: %w", err)
	}
	return requireRoutineChange(result)
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

const routineRunColumns = `id, routine_id, routine_name, prompt, directory, remote_id, trigger,
	platform, session_id, state, error, occurrence_at, created_at, started_at, finished_at`

func scanRoutineRun(row routineScanner) (RoutineRun, error) {
	var run RoutineRun
	err := row.Scan(&run.ID, &run.RoutineID, &run.RoutineName, &run.Prompt, &run.Directory, &run.RemoteID,
		&run.Trigger, &run.Platform, &run.SessionID, &run.State, &run.Error, &run.OccurrenceAt,
		&run.CreatedAt, &run.StartedAt, &run.FinishedAt)
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
	run.RoutineName, run.Prompt, run.Directory, run.RemoteID = routine.Name, routine.Prompt, routine.Directory, routine.RemoteID
	result, err := tx.ExecContext(ctx, `INSERT INTO routine_run (`+routineRunColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (routine_id, occurrence_at) DO NOTHING`, run.ID, run.RoutineID, run.RoutineName, run.Prompt,
		run.Directory, run.RemoteID, run.Trigger, run.Platform, run.SessionID, run.State, run.Error,
		run.OccurrenceAt, run.CreatedAt, run.StartedAt, run.FinishedAt)
	if err != nil {
		return RoutineRun{}, false, fmt.Errorf("claiming routine run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return RoutineRun{}, false, err
	}
	if changed == 0 {
		run, err = scanRoutineRun(tx.QueryRowContext(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE routine_id = ? AND occurrence_at = ?`, run.RoutineID, run.OccurrenceAt))
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
	rows, err := d.db.QueryContext(ctx, `SELECT `+routineRunColumns+` FROM routine_run WHERE routine_id = ? ORDER BY created_at DESC, id DESC`, routineID)
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
