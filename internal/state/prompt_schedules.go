package state

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrPromptScheduleNotFound = errors.New("prompt schedule not found")

type PromptSchedule struct {
	ID         string `json:"id"`
	Directory  string `json:"directory"`
	RemoteID   string `json:"remoteId"`
	Prompt     string `json:"prompt"`
	State      string `json:"state"`
	Platform   string `json:"platform,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Error      string `json:"error,omitempty"`
	RunAt      int64  `json:"runAt"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}

func (d *DB) CreatePromptSchedule(schedule PromptSchedule) error {
	_, err := d.db.Exec(`INSERT INTO prompt_schedule
		(id, directory, remote_id, prompt, run_at, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, schedule.ID, schedule.Directory, schedule.RemoteID, schedule.Prompt,
		schedule.RunAt, schedule.State, schedule.CreatedAt, schedule.UpdatedAt)
	if err != nil {
		return fmt.Errorf("creating prompt schedule: %w", err)
	}
	return nil
}

const promptScheduleColumns = `id, directory, remote_id, prompt, run_at, state, platform, session_id, error,
	created_at, updated_at, started_at, finished_at`

type promptScheduleScanner interface{ Scan(...any) error }

func scanPromptSchedule(row promptScheduleScanner) (PromptSchedule, error) {
	var s PromptSchedule
	err := row.Scan(&s.ID, &s.Directory, &s.RemoteID, &s.Prompt, &s.RunAt, &s.State, &s.Platform,
		&s.SessionID, &s.Error, &s.CreatedAt, &s.UpdatedAt, &s.StartedAt, &s.FinishedAt)
	return s, err
}

func (d *DB) GetPromptSchedule(id string) (PromptSchedule, error) {
	s, err := scanPromptSchedule(d.db.QueryRow(`SELECT `+promptScheduleColumns+` FROM prompt_schedule WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrPromptScheduleNotFound
	}
	if err != nil {
		return s, fmt.Errorf("getting prompt schedule: %w", err)
	}
	return s, nil
}

func (d *DB) ListPromptSchedules(directory, remoteID string) ([]PromptSchedule, error) {
	rows, err := d.db.Query(`SELECT `+promptScheduleColumns+` FROM prompt_schedule WHERE directory = ? AND remote_id = ? ORDER BY created_at DESC, id`, directory, remoteID)
	if err != nil {
		return nil, fmt.Errorf("listing prompt schedules: %w", err)
	}
	defer rows.Close()
	var out []PromptSchedule
	for rows.Next() {
		s, err := scanPromptSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning prompt schedule: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) ClaimPromptSchedule(id string, now int64, force bool) (PromptSchedule, bool, error) {
	query := `UPDATE prompt_schedule SET state = 'running', started_at = ?, updated_at = ?
		WHERE id = ? AND state = 'scheduled'`
	args := []any{now, now, id}
	if !force {
		query += ` AND run_at <= ?`
		args = append(args, now)
	}
	result, err := d.db.Exec(query, args...)
	if err != nil {
		return PromptSchedule{}, false, fmt.Errorf("claiming prompt schedule: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return PromptSchedule{}, false, err
	}
	if changed == 0 {
		s, getErr := d.GetPromptSchedule(id)
		return s, false, getErr
	}
	s, err := d.GetPromptSchedule(id)
	return s, err == nil, err
}

func (d *DB) ClaimNextDuePromptSchedule(now int64) (PromptSchedule, bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return PromptSchedule{}, false, fmt.Errorf("claiming due prompt schedule: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRow(`SELECT id FROM prompt_schedule WHERE state = 'scheduled' AND run_at <= ? ORDER BY run_at, id LIMIT 1`, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return PromptSchedule{}, false, nil
	}
	if err != nil {
		return PromptSchedule{}, false, err
	}
	result, err := tx.Exec(`UPDATE prompt_schedule SET state = 'running', started_at = ?, updated_at = ? WHERE id = ? AND state = 'scheduled'`, now, now, id)
	if err != nil {
		return PromptSchedule{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return PromptSchedule{}, false, err
	}
	s, err := scanPromptSchedule(tx.QueryRow(`SELECT `+promptScheduleColumns+` FROM prompt_schedule WHERE id = ?`, id))
	if err != nil {
		return PromptSchedule{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PromptSchedule{}, false, fmt.Errorf("committing due prompt schedule: %w", err)
	}
	return s, true, nil
}

func (d *DB) CancelPromptSchedule(id string, now int64) (PromptSchedule, bool, error) {
	result, err := d.db.Exec(`UPDATE prompt_schedule SET state = 'canceled', updated_at = ? WHERE id = ? AND state = 'scheduled'`, now, id)
	if err != nil {
		return PromptSchedule{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return PromptSchedule{}, false, err
	}
	if changed == 0 {
		s, getErr := d.GetPromptSchedule(id)
		return s, false, getErr
	}
	s, err := d.GetPromptSchedule(id)
	return s, err == nil, err
}

func (d *DB) LinkPromptScheduleSession(id, platform, sessionID string, now int64) error {
	_, err := d.db.Exec(`UPDATE prompt_schedule SET platform = ?, session_id = ?, updated_at = ? WHERE id = ? AND state = 'running'`, platform, sessionID, now, id)
	return err
}

func (d *DB) FinishPromptSchedule(id, stateValue, errorText string, now int64) error {
	_, err := d.db.Exec(`UPDATE prompt_schedule SET state = ?, error = ?, finished_at = ?, updated_at = ? WHERE id = ? AND state = 'running'`, stateValue, errorText, now, now, id)
	return err
}

func (d *DB) FailRunningPromptSchedules(now int64, errorText string) error {
	_, err := d.db.Exec(`UPDATE prompt_schedule SET state = 'failed', error = ?, finished_at = ?, updated_at = ? WHERE state = 'running'`, errorText, now, now)
	return err
}
