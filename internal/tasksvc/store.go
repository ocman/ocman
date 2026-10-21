package tasksvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// Schema belongs to this service. Conversation content remains in state_conv.db;
// generic task bookkeeping uses the existing module migration/backup mechanism.
var Schema = state.ModuleSchema{Module: "background-tasks", Tables: []string{"background_task"}, Migrations: []func(*sql.Tx) error{
	func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE background_task (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, task_key TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','interrupted')),
			updated_at INTEGER NOT NULL, data TEXT NOT NULL CHECK(json_valid(data))
		);
		CREATE UNIQUE INDEX background_task_active ON background_task(kind,task_key)
		WHERE task_key<>'' AND status IN ('queued','running');
		CREATE INDEX background_task_recent ON background_task(updated_at DESC,id DESC);`)
		return err
	},
}}

type store struct{ db *sql.DB }

func (s store) insert(ctx context.Context, task Task) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO background_task(id,kind,task_key,status,updated_at,data)
		VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, task.ID, task.Kind, task.Key, task.Status, task.UpdatedAt, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrBusy
	}
	return err
}

func (s store) save(ctx context.Context, task Task) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE background_task SET status=?,updated_at=?,data=? WHERE id=?`, task.Status, task.UpdatedAt, string(raw), task.ID)
	return err
}

func (s store) recover(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE background_task SET status='interrupted',updated_at=?,
		data=json_set(data,'$.status','interrupted','$.updatedAt',?,'$.finishedAt',?,'$.error','Ocman stopped before this task finished; work was not replayed.')
		WHERE status IN ('queued','running')`, now, now, now)
	return err
}

func (s store) prune(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM background_task WHERE status NOT IN ('queued','running') AND id NOT IN (
		SELECT id FROM background_task WHERE status NOT IN ('queued','running') ORDER BY updated_at DESC,id DESC LIMIT 1000)`)
	return err
}

func (m *Manager) List(ctx context.Context, status Status, kind string, limit int) ([]Task, error) {
	if !m.Enabled() {
		return nil, ErrUnavailable
	}
	if limit < 1 || limit > 200 || (status != "" && !ValidStatus(status)) {
		return nil, ErrProgress
	}
	rows, err := m.store.db.QueryContext(ctx, `SELECT data FROM background_task
		WHERE (?='' OR status=?) AND (?='' OR kind=?)
		ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END,updated_at DESC,id DESC LIMIT ?`, status, status, kind, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []Task{}
	for rows.Next() {
		var raw string
		var task Task
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (m *Manager) Get(ctx context.Context, id string) (Task, error) {
	var task Task
	if !m.Enabled() {
		return task, ErrUnavailable
	}
	var raw string
	err := m.store.db.QueryRowContext(ctx, `SELECT data FROM background_task WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return task, ErrNotFound
	}
	if err != nil {
		return task, err
	}
	err = json.Unmarshal([]byte(raw), &task)
	return task, err
}
