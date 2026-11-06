package tasksvc

import (
	"context"
	"encoding/json"
)

func (s store) beginScheduled(ctx context.Context, task Task) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO background_task(id,kind,task_key,status,updated_at,data) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at,data=excluded.data
		WHERE background_task.status NOT IN ('queued','running')`, task.ID, task.Kind, task.Key, task.Status, task.UpdatedAt, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrBusy
	}
	return err
}
