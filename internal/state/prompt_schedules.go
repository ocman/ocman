package state

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrPromptScheduleNotFound   = errors.New("prompt schedule not found")
	ErrPromptScheduleSuperseded = errors.New("prompt schedule state changed")
)

type PromptSchedule struct {
	ID              string `json:"id"`
	Directory       string `json:"directory"`
	RemoteID        string `json:"remoteId"`
	Prompt          string `json:"prompt"`
	State           string `json:"state"`
	Platform        string `json:"platform,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	Error           string `json:"error,omitempty"`
	RunAt           int64  `json:"runAt"`
	TimingType      string `json:"timingType"`
	IntervalMinutes int64  `json:"intervalMinutes,omitempty"`
	Cron            string `json:"cron,omitempty"`
	Timezone        string `json:"timezone"`
	Enabled         bool   `json:"enabled"`
	SessionMode     string `json:"sessionMode"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	StartedAt       int64  `json:"startedAt,omitempty"`
	FinishedAt      int64  `json:"finishedAt,omitempty"`
}

func (d *DB) CreatePromptSchedule(schedule PromptSchedule) error {
	_, err := d.db.Exec(`INSERT INTO prompt_schedule
		(id, directory, remote_id, prompt, run_at, state, timing_type, interval_minutes, cron, timezone, enabled, session_mode, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, schedule.ID, schedule.Directory, schedule.RemoteID, schedule.Prompt,
		schedule.RunAt, schedule.State, schedule.TimingType, schedule.IntervalMinutes, schedule.Cron, schedule.Timezone,
		schedule.Enabled, schedule.SessionMode, schedule.CreatedAt, schedule.UpdatedAt)
	if err != nil {
		return fmt.Errorf("creating prompt schedule: %w", err)
	}
	return nil
}

const promptScheduleColumns = `id, directory, remote_id, prompt, run_at, state, platform, session_id, error,
	timing_type, interval_minutes, cron, timezone, enabled, session_mode,
	created_at, updated_at, started_at, finished_at`

type promptScheduleScanner interface{ Scan(...any) error }

func scanPromptSchedule(row promptScheduleScanner) (PromptSchedule, error) {
	var s PromptSchedule
	err := row.Scan(&s.ID, &s.Directory, &s.RemoteID, &s.Prompt, &s.RunAt, &s.State, &s.Platform,
		&s.SessionID, &s.Error, &s.TimingType, &s.IntervalMinutes, &s.Cron, &s.Timezone, &s.Enabled,
		&s.SessionMode, &s.CreatedAt, &s.UpdatedAt, &s.StartedAt, &s.FinishedAt)
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

func (d *DB) ListRunningPromptSchedules() ([]PromptSchedule, error) {
	rows, err := d.db.Query(`SELECT ` + promptScheduleColumns + ` FROM prompt_schedule WHERE state = 'running' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing running prompt schedules: %w", err)
	}
	defer rows.Close()
	var out []PromptSchedule
	for rows.Next() {
		s, err := scanPromptSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning running prompt schedule: %w", err)
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

// ClaimNextDuePromptSchedule skips archived work without changing its due time,
// so an overdue schedule becomes eligible immediately after unarchive.
func (d *DB) ClaimNextDuePromptSchedule(now int64) (PromptSchedule, bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return PromptSchedule{}, false, fmt.Errorf("claiming due prompt schedule: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	type candidate struct{ id, directory, sessionMode, platform, sessionID string }
	rows, err := tx.Query(`SELECT id, directory, session_mode, platform, session_id FROM prompt_schedule WHERE state = 'scheduled' AND enabled = 1 AND run_at <= ? ORDER BY run_at, id`, now)
	if err != nil {
		return PromptSchedule{}, false, err
	}
	var candidates []candidate
	for rows.Next() {
		var candidate candidate
		if err := rows.Scan(&candidate.id, &candidate.directory, &candidate.sessionMode, &candidate.platform, &candidate.sessionID); err != nil {
			_ = rows.Close()
			return PromptSchedule{}, false, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return PromptSchedule{}, false, err
	}
	if err := rows.Close(); err != nil {
		return PromptSchedule{}, false, err
	}
	var id string
	for _, candidate := range candidates {
		var archived int
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM archived_project WHERE project_root = ?)`, ProjectRootForDirectory(candidate.directory)).Scan(&archived)
		if err != nil {
			return PromptSchedule{}, false, err
		}
		if archived != 0 {
			continue
		}
		if candidate.sessionMode == "reuse" && candidate.platform != "" && candidate.sessionID != "" {
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM archived_session WHERE platform = ? AND session_id = ?)`, candidate.platform, candidate.sessionID).Scan(&archived)
			if err != nil {
				return PromptSchedule{}, false, err
			}
			if archived != 0 {
				continue
			}
		}
		id = candidate.id
		break
	}
	if id == "" {
		return PromptSchedule{}, false, nil
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
	result, err := d.db.Exec(`UPDATE prompt_schedule SET state = 'canceled', finished_at = CASE WHEN state = 'running' THEN ? ELSE finished_at END, updated_at = ? WHERE id = ? AND state IN ('scheduled', 'running')`, now, now, id)
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
	result, err := d.db.Exec(`UPDATE prompt_schedule SET platform = ?, session_id = ?, updated_at = ? WHERE id = ? AND state = 'running'`, platform, sessionID, now, id)
	return promptScheduleUpdate(result, err)
}

func (d *DB) FinishPromptSchedule(id, stateValue, errorText string, now int64) error {
	result, err := d.db.Exec(`UPDATE prompt_schedule SET state = ?, error = ?, finished_at = ?, updated_at = ? WHERE id = ? AND state = 'running'`, stateValue, errorText, now, now, id)
	return promptScheduleUpdate(result, err)
}

func (d *DB) CompletePromptSchedule(id string, nextRunAt, now int64) error {
	result, err := d.db.Exec(`UPDATE prompt_schedule SET state = 'scheduled', run_at = ?, error = '', finished_at = ?, updated_at = ? WHERE id = ? AND state = 'running'`, nextRunAt, now, now, id)
	return promptScheduleUpdate(result, err)
}

func (d *DB) ReschedulePromptSchedule(id string, nextRunAt int64, errorText string, now int64) error {
	result, err := d.db.Exec(`UPDATE prompt_schedule SET state = 'scheduled', run_at = ?, error = ?, finished_at = ?, updated_at = ? WHERE id = ? AND state = 'running'`, nextRunAt, errorText, now, now, id)
	return promptScheduleUpdate(result, err)
}

func (d *DB) SetPromptScheduleEnabled(id string, enabled bool, runAt, now int64) (PromptSchedule, error) {
	var err error
	if enabled {
		_, err = d.db.Exec(`UPDATE prompt_schedule SET enabled = 1, run_at = ?, state = 'scheduled', error = '', updated_at = ? WHERE id = ? AND state != 'running'`, runAt, now, id)
	} else {
		_, err = d.db.Exec(`UPDATE prompt_schedule SET enabled = 0, run_at = ?, state = CASE WHEN state = 'running' THEN 'scheduled' ELSE state END, finished_at = CASE WHEN state = 'running' THEN ? ELSE finished_at END, updated_at = ? WHERE id = ?`, runAt, now, now, id)
	}
	if err != nil {
		return PromptSchedule{}, err
	}
	return d.GetPromptSchedule(id)
}

func promptScheduleUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrPromptScheduleSuperseded
	}
	return nil
}

func (d *DB) FailRunningPromptSchedules(now int64, errorText string) error {
	_, err := d.db.Exec(`UPDATE prompt_schedule SET state = 'failed', error = ?, finished_at = ?, updated_at = ? WHERE state = 'running'`, errorText, now, now)
	return err
}
